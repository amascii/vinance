#!/usr/bin/env bash
# Runs vinance as a background service on this Mac (a launchd LaunchAgent) and updates it.
#
#   scripts/service.sh install [--dry-run]   build, copy the DB, write the LaunchAgent, start it
#   scripts/service.sh deploy                build main, back up the DB, swap the binary, restart (rolls back if unhealthy)
#   scripts/service.sh status                is it loaded, and does /healthz answer
#   scripts/service.sh uninstall             stop it and remove the LaunchAgent (data is kept)
#
# Everything the service owns lives outside the repo, so dev work (`make run`, tests) can't touch the real data:
#   ~/Library/Application Support/vinance/{vinance.db, bin/, backups/}   ~/Library/Logs/vinance/vinance.log
#   ~/Library/LaunchAgents/com.amascii.vinance.plist  (holds the password hash; mode 600)
#
# Settings (environment):
#   VINANCE_PASSWORD_HASH   bcrypt hash for the login; asked for if unset and not already in the installed plist
#   VINANCE_SECURE_COOKIES  default true: the service is meant to be reached over HTTPS (tailscale serve)
#   VINANCE_SERVICE_ADDR    default 127.0.0.1:8787 (not 8080, so `make run` keeps working next to it)
#   VINANCE_SERVICE_HOME    default $HOME (tests point it at a temp dir)
#   VINANCE_SERVICE_LABEL   default com.amascii.vinance (a different label lets you try the script without touching the real service)
#   VINANCE_SERVICE_SOURCE_DB  database to copy on first install, default <repo>/data/vinance.db
#   VINANCE_SERVICE_NO_LAUNCHD=1  write files only, never call launchctl or wait for health (tests)
set -euo pipefail

LABEL=${VINANCE_SERVICE_LABEL:-com.amascii.vinance}
REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
HOME_DIR=${VINANCE_SERVICE_HOME:-$HOME}
ADDR=${VINANCE_SERVICE_ADDR:-127.0.0.1:8787}
SECURE=${VINANCE_SECURE_COOKIES:-true}
SOURCE_DB=${VINANCE_SERVICE_SOURCE_DB:-$REPO/data/vinance.db}

DATA="$HOME_DIR/Library/Application Support/vinance"
BIN="$DATA/bin/vinance"
DB="$DATA/vinance.db"
BACKUPS="$DATA/backups"
LOGDIR="$HOME_DIR/Library/Logs/vinance"
PLIST="$HOME_DIR/Library/LaunchAgents/$LABEL.plist"
DOMAIN="gui/$(id -u)"
DRY=0

say() { printf '%s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }
run() { if [ "$DRY" = 1 ]; then say "[dry-run] $*"; else "$@"; fi; }
skip_launchd() { [ "${VINANCE_SERVICE_NO_LAUNCHD:-}" = 1 ]; }

build() { # build <output path>: atomic enough, the old binary stays until mv
	run mkdir -p "$(dirname "$1")"
	say "building $1"
	run bash -c 'cd "$1" && go build -o "$2" ./cmd/vinance' _ "$REPO" "$1"
}

copy_db() { # copy_db <source> <destination>
	command -v sqlite3 >/dev/null || die "sqlite3 not found"
	run sqlite3 "$1" ".backup '$2'"
}

existing_hash() { # the hash in an installed plist, so re-installing does not ask again
	[ -f "$PLIST" ] && command -v plutil >/dev/null &&
		plutil -extract EnvironmentVariables.VINANCE_PASSWORD_HASH raw -o - "$PLIST" 2>/dev/null || true
}

password_hash() {
	local h=${VINANCE_PASSWORD_HASH:-}
	[ -n "$h" ] || h=$(existing_hash)
	if [ -z "$h" ]; then
		[ -t 0 ] || die "VINANCE_PASSWORD_HASH is not set (make one with: go run ./cmd/vinance hash-password)"
		read -rsp "Password hash (paste it, or leave empty to type a password and make one): " h >&2 || true
		printf '\n' >&2
		if [ -z "$h" ]; then
			h=$(cd "$REPO" && go run ./cmd/vinance hash-password)
		fi
	fi
	case $h in '$2'[aby]'$'*) ;; *) die "that is not a bcrypt hash (it should start with \$2a\$, \$2b\$ or \$2y\$)" ;; esac
	printf '%s' "$h"
}

write_plist() { # write_plist <hash>
	local hash=$1
	[ "$DRY" = 1 ] && { say "[dry-run] write $PLIST"; return; }
	mkdir -p "$(dirname "$PLIST")" "$LOGDIR"
	local tmp
	tmp=$(mktemp "$PLIST.XXXXXX")
	chmod 600 "$tmp"
	# bcrypt hashes use only [./A-Za-z0-9$], so they need no XML escaping.
	cat >"$tmp" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key>
	<array><string>$BIN</string><string>serve</string></array>
	<key>WorkingDirectory</key><string>$DATA</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>VINANCE_DB</key><string>$DB</string>
		<key>VINANCE_ADDR</key><string>$ADDR</string>
		<key>VINANCE_PASSWORD_HASH</key><string>$hash</string>
		<key>VINANCE_SECURE_COOKIES</key><string>$SECURE</string>
		<key>LOG_LEVEL</key><string>info</string>
	</dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>ThrottleInterval</key><integer>10</integer>
	<key>StandardOutPath</key><string>$LOGDIR/vinance.log</string>
	<key>StandardErrorPath</key><string>$LOGDIR/vinance.log</string>
</dict>
</plist>
EOF
	mv "$tmp" "$PLIST"
}

healthy() { # wait up to ~15s for /healthz
	local i
	for i in $(seq 1 30); do
		curl -fsS "http://$ADDR/healthz" >/dev/null 2>&1 && return 0
		sleep 0.5
	done
	return 1
}

load() { # (re)load the agent from the plist
	skip_launchd && return 0
	launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
	launchctl bootstrap "$DOMAIN" "$PLIST"
}

cmd_install() {
	[ "${1:-}" = "--dry-run" ] && DRY=1
	[ "$(uname)" = Darwin ] || skip_launchd || die "this installs a macOS LaunchAgent"

	local hash
	hash=$(password_hash)

	if [ -f "$DB" ]; then
		say "keeping the existing database $DB"
	elif [ -f "$SOURCE_DB" ]; then
		say "copying $SOURCE_DB to $DB (the original stays where it is)"
		run mkdir -p "$DATA"
		copy_db "$SOURCE_DB" "$DB"
	else
		say "no database to copy: the app will create an empty one at $DB"
	fi

	build "$BIN"
	write_plist "$hash"
	[ "$DRY" = 1 ] && { say "[dry-run] launchctl bootstrap $DOMAIN $PLIST"; return; }
	load
	if skip_launchd; then say "files written (launchd skipped)"; return; fi
	healthy || die "the service did not become healthy; see $LOGDIR/vinance.log"
	say "running at http://$ADDR. Next: tailscale serve --bg ${ADDR##*:}"
}

cmd_deploy() {
	[ -f "$PLIST" ] || die "not installed; run: scripts/service.sh install"
	cd "$REPO"
	[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || die "deploy only from main (you are on $(git rev-parse --abbrev-ref HEAD))"
	[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
	git fetch -q origin main
	[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "main is not the same as origin/main; pull first (or you have unpushed commits)"

	mkdir -p "$BACKUPS"
	local stamp
	stamp=$(date +%Y%m%d-%H%M%S)
	say "backing up the database to $BACKUPS/pre-deploy-$stamp.db"
	copy_db "$DB" "$BACKUPS/pre-deploy-$stamp.db"
	# keep the 10 newest pre-deploy backups
	(cd "$BACKUPS" && ls -1t pre-deploy-*.db 2>/dev/null | tail -n +11 | while read -r f; do rm -f -- "$f"; done)

	[ -f "$BIN" ] && cp "$BIN" "$BIN.prev"
	build "$BIN.new"
	mv "$BIN.new" "$BIN"
	skip_launchd && { say "binary replaced (launchd skipped)"; return; }
	launchctl kickstart -k "$DOMAIN/$LABEL"
	if healthy; then
		say "deployed $(git rev-parse --short HEAD); healthy at http://$ADDR"
		return
	fi
	say "new build did not become healthy; rolling back the binary"
	if [ -f "$BIN.prev" ]; then
		mv "$BIN.prev" "$BIN"
		launchctl kickstart -k "$DOMAIN/$LABEL"
		if healthy; then say "rolled back: the previous build is serving again"; else say "the previous build is not healthy either"; fi
	fi
	die "deploy failed; see $LOGDIR/vinance.log. If a migration ran, the pre-deploy backup is $BACKUPS/pre-deploy-$stamp.db"
}

cmd_status() {
	if launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; then say "launchd: loaded"; else say "launchd: not loaded"; fi
	if curl -fsS "http://$ADDR/healthz" 2>/dev/null; then printf '\n'; else say "http://$ADDR/healthz: no answer"; fi
	say "log: $LOGDIR/vinance.log"
}

cmd_uninstall() {
	skip_launchd || launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
	rm -f "$PLIST"
	say "removed $PLIST. Your data is untouched: $DATA"
}

case ${1:-} in
install) shift; cmd_install "$@" ;;
deploy) cmd_deploy ;;
status) cmd_status ;;
uninstall) cmd_uninstall ;;
*) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
