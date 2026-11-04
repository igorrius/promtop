#!/bin/sh
# Installs promtop from the GitHub releases, or updates it to the latest one.
# Linux, macOS and FreeBSD; on Windows use install.ps1. Safe to run again.
#
#   curl -fsSL https://raw.githubusercontent.com/igorrius/promtop/main/scripts/install.sh | sh
#
# Environment:
#   PROMTOP_VERSION      release to install, e.g. v0.5.0 (default: the latest)
#   PROMTOP_INSTALL_DIR  where to put the binary (default: next to the promtop
#                        already on PATH if that directory is writable, except
#                        /usr/bin and /bin, which belong to the Linux packages;
#                        otherwise ~/.local/bin, or /usr/local/bin for root)
set -eu

repo=igorrius/promtop
tmp=
staged=

warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

fetch() { # url file
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

sha256() { # file
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v sha256 >/dev/null 2>&1; then
		sha256 -q "$1"
	else
		die "sha256sum, shasum or sha256 is required to verify the download"
	fi
}

detect_os() {
	case $(uname -s) in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	FreeBSD) echo freebsd ;;
	*) die "unsupported system $(uname -s); on Windows use install.ps1" ;;
	esac
}

detect_arch() {
	case $(uname -m) in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	armv7* | armv8l) echo armv7 ;;
	*) die "unsupported architecture $(uname -m)" ;;
	esac
}

default_dir() {
	existing=$(command -v promtop 2>/dev/null || true)
	if [ -n "$existing" ]; then
		dir=$(dirname "$existing")
		case $dir in
		/usr/bin | /bin) ;;
		*)
			if [ -w "$dir" ]; then
				echo "$dir"
				return
			fi
			;;
		esac
	fi
	if [ "$(id -u)" = 0 ]; then
		echo /usr/local/bin
	else
		echo "${HOME:?HOME is not set}/.local/bin"
	fi
}

# The flag and not the subcommand: a build too old to know "version" would take
# it for a scrape target, while an unknown flag just fails.
version_of() { # binary
	"$1" -version </dev/null 2>/dev/null | awk '$1 == "promtop" { print $2 }' || true
}

check_path() { # dir
	found=$(command -v promtop 2>/dev/null || true)
	if [ -z "$found" ]; then
		# shellcheck disable=SC2016 # $PATH is for the user's shell to expand
		printf 'Add it to PATH to run it by name:\n  export PATH="%s:$PATH"\n' "$1"
	elif [ "$found" != "$1/promtop" ]; then
		warn "$found comes first on PATH and is the one that runs"
	fi
}

main() {
	os=$(detect_os)
	arch=$(detect_arch)

	if [ -n "${PROMTOP_VERSION:-}" ]; then
		base="https://github.com/$repo/releases/download/v${PROMTOP_VERSION#v}"
	else
		base="https://github.com/$repo/releases/latest/download"
	fi

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp" "$staged"' EXIT
	trap 'exit 1' HUP INT TERM

	# checksums.txt names every archive, so it gives the version as well. A release
	# gets its files a few minutes after the tag, hence the second reason below.
	fetch "$base/checksums.txt" "$tmp/checksums.txt" ||
		die "cannot download $base/checksums.txt: no network, no such release, or it is still being built"

	line=$(awk -v re="^promtop_[^_]+_${os}_${arch}[.]tar[.]gz\$" '$2 ~ re' "$tmp/checksums.txt")
	[ -n "$line" ] || die "this release has no build for $os/$arch"
	want=${line%% *}
	archive=${line##* }
	version=${archive#promtop_}
	version=${version%%_*}

	dir=${PROMTOP_INSTALL_DIR:-$(default_dir)}
	[ "$dir" = / ] || dir=${dir%/}
	target=$dir/promtop

	current=
	if [ -x "$target" ]; then
		current=$(version_of "$target")
	fi
	if [ "$current" = "$version" ]; then
		echo "promtop $version is already installed in $dir"
		check_path "$dir"
		return
	fi

	mkdir -p "$dir" 2>/dev/null || die "cannot create $dir; set PROMTOP_INSTALL_DIR or run with sudo"
	[ -w "$dir" ] || die "$dir is not writable; set PROMTOP_INSTALL_DIR or run with sudo"

	echo "Downloading $archive"
	fetch "https://github.com/$repo/releases/download/v$version/$archive" "$tmp/$archive" ||
		die "cannot download $archive"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || die "checksum mismatch for $archive: expected $want, got $got"
	tar -xzf "$tmp/$archive" -C "$tmp" promtop

	# Staged next to the target: the rename is atomic and works while the old binary
	# is running, and the new one is tried on this directory's mount, not on /tmp.
	staged=$dir/.promtop.$$
	cp "$tmp/promtop" "$staged"
	chmod 755 "$staged"
	[ "$(version_of "$staged")" = "$version" ] || die "the downloaded binary does not run on this system"
	mv -f "$staged" "$target"

	if [ -n "$current" ]; then
		echo "promtop updated from $current to $version in $dir"
	else
		echo "promtop $version installed in $dir"
	fi
	check_path "$dir"
}

# Last on purpose: a download cut short runs nothing rather than half a script.
main
