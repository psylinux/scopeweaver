#!/bin/sh
# ScopeWeaver supervisor: exit 0 stops, 75 restarts immediately, other codes back off.
# Forward termination signals and wait for the child to finish graceful shutdown.
# Download verification and atomic replacement are handled by selfupdate.Bootstrap.
set -u
LANG_SEL="${SCOPEWEAVER_LANGUAGE:-${ARTEX_LANGUAGE:-en}}"
case "$LANG_SEL" in ko|ko_*|ko-*|KO) LANG_SEL=ko ;; *) LANG_SEL=en ;; esac
msg(){ if [ "$LANG_SEL" = ko ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }


cd "$(dirname "$0")" || exit 1

BIN=./scopeweaver
[ -x "$BIN" ] || { echo "[ScopeWeaver] $(msg "Executable not found: $BIN" "실행 파일을 찾을 수 없습니다: $BIN")" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0
mailotp_sup=0

# mailotp serve: email OTP sync sidecar (TICKET-007). One IMAP talker per host,
# syncing the controlled mailboxes into a local store that agents read over a
# unix socket. Best-effort: if it is down, the email-otp skill falls back to a
# direct IMAP read. Supervised in a subshell so a crash restarts it; the subshell
# forwards a signal to the running daemon so shutdown is graceful.
MAILOTP_BIN=/usr/local/bin/mailotp
if [ -x "$MAILOTP_BIN" ]; then
	(
		trap 'exit 0' INT TERM
		while :; do
			"$MAILOTP_BIN" serve &
			sp=$!
			trap 'kill -TERM "$sp" 2>/dev/null; exit 0' INT TERM
			wait "$sp"
			sleep 2
		done
	) &
	mailotp_sup=$!
	echo "[ScopeWeaver] $(msg "mailotp serve sidecar started (pid $mailotp_sup)" "mailotp serve 사이드카 시작 (pid $mailotp_sup)")"
fi

forward() {
	stopping=1
	if [ "$mailotp_sup" -ne 0 ]; then
		kill -TERM "$mailotp_sup" 2>/dev/null || true
	fi
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[ScopeWeaver] $(msg "Stopped" "중지됨")"
		exit 0
	fi

	case "$code" in
		0)
			echo "[ScopeWeaver] $(msg "Exited normally" "정상 종료")"
			exit 0
			;;
		"$RESTART_CODE")
			echo "[ScopeWeaver] $(msg "Restart requested (applying update)…" "다시 시작 요청 (업데이트 적용 중)…")"
			delay=1
			;;
		*)
			echo "[ScopeWeaver] $(msg "Exited with code $code; restarting in ${delay}s" "종료 코드 $code; ${delay}초 후 다시 시작")" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
