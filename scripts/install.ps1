# Installs promtop from the GitHub releases, or updates it to the latest one.
# Windows, PowerShell 5.1 or later; elsewhere use install.sh. Safe to run again.
#
#   irm https://raw.githubusercontent.com/igorrius/promtop/main/scripts/install.ps1 | iex
#
# Environment:
#   PROMTOP_VERSION      release to install, e.g. v0.5.0 (default: the latest)
#   PROMTOP_INSTALL_DIR  where to put the binary (default: next to the promtop
#                        already on PATH if that directory is writable, otherwise
#                        %LOCALAPPDATA%\Programs\promtop, which is added to the
#                        user's PATH)
#
# Run through iex this shares the caller's session: errors are thrown, because
# exit would close the window, and everything stays inside the function.

function Install-Promtop {
    $ErrorActionPreference = 'Stop'
    # Invoke-WebRequest is many times slower with its progress bar on.
    $ProgressPreference = 'SilentlyContinue'
    # Windows PowerShell 5.1 may not offer TLS 1.2 by default; GitHub requires it.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'igorrius/promtop'

    # Tells a 32-bit PowerShell on 64-bit Windows from a 32-bit system.
    $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($machine) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "unsupported architecture $machine" }
    }

    # To a file, always: GitHub serves these as binary, which would make a
    # response's Content a byte array. -UseBasicParsing is for PowerShell 5.1.
    function Get-Url([string]$Url, [string]$File) {
        Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $File
    }

    function Test-Writable([string]$Dir) {
        $probe = Join-Path $Dir ".promtop-$([guid]::NewGuid())"
        try {
            [IO.File]::Create($probe).Dispose()
            Remove-Item -Force $probe
            return $true
        } catch {
            return $false
        }
    }

    # The flag and not the subcommand: a build too old to know "version" would take
    # it for a scrape target, while an unknown flag just fails.
    function Get-PromtopVersion([string]$Exe) {
        try {
            $out = $null | & $Exe -version 2>$null
            if ("$out" -match '^promtop\s+(\S+)') { return $Matches[1] }
        } catch {}
        return ''
    }

    function Find-Promtop {
        $cmd = Get-Command promtop.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($cmd) { return $cmd.Source }
        return ''
    }

    # Through the registry: [Environment]::SetEnvironmentVariable would expand
    # %VARIABLES% in the user's PATH for good. Setting and clearing a variable the
    # official way then tells running programs that the environment has changed.
    function Add-ToUserPath([string]$Dir) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
        try {
            $path = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $key.SetValue('Path', (@($path.TrimEnd(';'), $Dir) -ne '' -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally {
            $key.Dispose()
        }
        [Environment]::SetEnvironmentVariable('PROMTOP_INSTALL_NOTIFY', '1', 'User')
        [Environment]::SetEnvironmentVariable('PROMTOP_INSTALL_NOTIFY', $null, 'User')
    }

    # Only the default directory goes on the user's PATH for good. One named by hand,
    # as promtop update does for wherever it was run from, may be anything: Downloads.
    function Update-PathEntry([string]$Dir, [bool]$AddToPath) {
        $target = Join-Path $Dir 'promtop.exe'
        $found = Find-Promtop
        if ($found -and $found -ne $target) {
            Write-Warning "$found comes first on PATH and is the one that runs"
        }
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @("$userPath" -split ';' | ForEach-Object { $_.TrimEnd('\') })
        if ($entries -notcontains $Dir.TrimEnd('\') -and -not ($found -eq $target)) {
            if ($AddToPath) {
                Add-ToUserPath $Dir
                $env:Path = "$env:Path;$Dir"
                Write-Host "Added $Dir to your PATH; other open terminals need a restart to see it"
            } elseif (-not $found) {
                Write-Host "Add $Dir to your PATH to run it by name"
            }
        }
    }

    if ($env:PROMTOP_VERSION) {
        $base = "https://github.com/$repo/releases/download/v$($env:PROMTOP_VERSION -replace '^v', '')"
    } else {
        $base = "https://github.com/$repo/releases/latest/download"
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) "promtop-$([guid]::NewGuid())"
    New-Item -ItemType Directory -Path $tmp | Out-Null
    $staged = ''
    try {
        # checksums.txt names every archive, so it gives the version as well. A release
        # gets its files a few minutes after the tag, hence the second reason below.
        $checksums = Join-Path $tmp 'checksums.txt'
        try {
            Get-Url "$base/checksums.txt" $checksums
        } catch {
            throw "cannot download $base/checksums.txt (no network, no such release, or it is still being built): $_"
        }

        $archive = ''
        $want = ''
        foreach ($line in Get-Content $checksums) {
            $fields = -split $line
            if ($fields.Count -eq 2 -and $fields[1] -match "^promtop_[^_]+_windows_$arch\.zip$") {
                $want = $fields[0]
                $archive = $fields[1]
            }
        }
        if (-not $archive) { throw "this release has no build for windows/$arch" }
        $version = ($archive -split '_')[1]

        $dir = $env:PROMTOP_INSTALL_DIR
        $addToPath = $false
        if (-not $dir) {
            $existing = Find-Promtop
            if ($existing -and (Test-Writable (Split-Path $existing))) {
                $dir = Split-Path $existing
            } else {
                $dir = Join-Path $env:LOCALAPPDATA 'Programs\promtop'
                $addToPath = $true
            }
        }
        $dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir).TrimEnd('\', '/')
        $target = Join-Path $dir 'promtop.exe'

        $current = ''
        if (Test-Path $target) { $current = Get-PromtopVersion $target }
        if ($current -eq $version) {
            Write-Host "promtop $version is already installed in $dir"
            Update-PathEntry $dir $addToPath
            return
        }

        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        if (-not (Test-Writable $dir)) { throw "$dir is not writable; set PROMTOP_INSTALL_DIR or run as administrator" }

        Write-Host "Downloading $archive"
        $zip = Join-Path $tmp $archive
        Get-Url "https://github.com/$repo/releases/download/v$version/$archive" $zip
        $got = (Get-FileHash -Algorithm SHA256 $zip).Hash
        if ($got -ne $want) { throw "checksum mismatch for ${archive}: expected $want, got $got" }
        Expand-Archive -Path $zip -DestinationPath (Join-Path $tmp 'x')

        # A running exe cannot be overwritten or deleted but can be renamed, so it
        # moves aside; if it is still running its .old stays until the next update.
        $staged = Join-Path $dir "promtop.new.exe"
        Copy-Item -Force (Join-Path $tmp 'x\promtop.exe') $staged
        if ((Get-PromtopVersion $staged) -ne $version) { throw 'the downloaded binary does not run on this system' }
        $old = "$target.old"
        Remove-Item -Force $old -ErrorAction SilentlyContinue
        $hadOld = Test-Path $target
        if ($hadOld) { Move-Item -Force $target $old }
        try {
            Move-Item -Force $staged $target
        } catch {
            if ($hadOld) { Move-Item -Force $old $target }
            throw
        }
        Remove-Item -Force $old -ErrorAction SilentlyContinue

        if ($current) {
            Write-Host "promtop updated from $current to $version in $dir"
        } else {
            Write-Host "promtop $version installed in $dir"
        }
        Update-PathEntry $dir $addToPath
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        if ($staged) { Remove-Item -Force $staged -ErrorAction SilentlyContinue }
    }
}

Install-Promtop
Remove-Item Function:\Install-Promtop
