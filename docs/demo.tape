# Records docs/demo.gif. Run from the repository root:
#   DEMO_TMP=/private/tmp/sf-demo docs/screenshots/demo-world.sh build   # once
#   DEMO_TMP=/private/tmp/sf-demo vhs docs/demo.tape
# Needs vhs, ttyd, ffmpeg, Go, Docker and npm. Only the example projects acme-web
# and acme-api (copies of examples/) are shown; temp paths are rewritten to ~/.
# See docs/screenshots/README.md for the demo world.
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1100
Set Height 680
Set Padding 16
Set Framerate 12
Set PlaybackSpeed 1.2
Set TypingSpeed 35ms
Set Theme "Dracula"

Hide
Type `export T=$DEMO_TMP PS1='\W $ ' && . $T/env.sh` Enter
Type `stackfork() { command stackfork --state-dir $T/state "$@" 2>&1 | grep -v -E '^ (Container|Network|Volume|Image) |^#[0-9]+ |^$' | sed "s#$T/code/#~/#g; s#$T/#~/#g; s#LEGACY  ##; s#  -  *~/#  ~/#" | cut -c1-104; return ${PIPESTATUS[0]}; }` Enter
Type `docs/screenshots/demo-world.sh start >/dev/null 2>&1; docs/screenshots/demo-world.sh reset; cd $T/code/acme-web && clear` Enter
Wait+Line@240s /acme-web \$ ?$/
Show

Type `stackfork learn` Enter
Sleep 4s
Type `clear` Enter
Type `stackfork start-env && stackfork runs show` Enter
Wait+Screen@120s /seed +(succeeded|skipped)/
Sleep 4s
Type `clear` Enter
Type `stackfork worktree add . feature-invoices --from main && cd .worktrees/feature-invoices` Enter
Wait+Line@60s /feature-invoices \$ ?$/
Type `stackfork start-env && stackfork runs show` Enter
Wait+Screen@120s /seed +(succeeded|skipped)/
Sleep 3s
Type `clear` Enter
Type `stackfork list` Enter
Sleep 3s
Type `stackfork urls` Enter
Sleep 2s
Type `cd ../.. && stackfork urls` Enter
Sleep 5s

Hide
Type `docs/screenshots/demo-world.sh reset >/dev/null 2>&1; echo cleaned-$((1+1))` Enter
Wait+Screen@240s /cleaned-2/
