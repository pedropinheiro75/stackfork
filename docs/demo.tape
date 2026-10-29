# Records docs/demo.gif. Run from the repository root:
#   DEMO_TMP=/private/tmp/sf-demo docs/screenshots/demo-world.sh build   # once
#   DEMO_TMP=/private/tmp/sf-demo vhs docs/demo.tape
# Needs vhs, ttyd, ffmpeg, Go, Docker and npm. Only the example projects acme-web
# and acme-api (copies of examples/) are shown; temp paths are rewritten to ~/.
# See docs/screenshots/README.md for the demo world.
# Story: two branches of one project run side by side; each answers with the
# visit count of ITS OWN database (seeded with 120 rows), so the counts diverge.
# The waits for `start-env` happen between Hide and Show: the viewer sees the output only.
Output docs/demo.gif
Set Shell bash
Set FontSize 22
Set Width 1000
Set Height 340
Set Padding 16
Set Framerate 12
Set TypingSpeed 30ms
Set Theme "Dracula"

Hide
Type `export T=$DEMO_TMP PS1='\W $ ' && . $T/env.sh` Enter
Type `stackfork() { command stackfork --state-dir $T/state "$@" 2>&1 | grep -v -E '^ (Container|Network|Volume|Image) |^#[0-9]+ |^$|seed|INSERT' | sed -E "s#$T/code/#~/#g; s#$T/#~/#g; s#(sf-[a-z-]+)-[0-9a-f]{12}#\1#g; s#; see .*##" | if [ "$1" = list ]; then awk '{print $3,$2,$NF}' | column -t; else cat; fi | cut -c1-72; return ${PIPESTATUS[0]}; }` Enter
Type `docs/screenshots/demo-world.sh reset >/dev/null 2>&1; cd $T/code/acme-web && clear` Enter
Wait+Line@240s /acme-web \$ ?$/
Show

Type "# one project, two branches, one laptop"
Enter
Sleep 600ms
Type `stackfork start-env` Enter
Hide
Wait+Screen@120s /started sf-/
Show
Sleep 800ms
Type `stackfork urls | head -1` Enter
Sleep 3s
Ctrl+L

Type `stackfork worktree add . feature-invoices --from main && cd .worktrees/feature-invoices` Enter
Wait+Line@60s /feature-invoices \$ ?$/
Sleep 500ms
Type `stackfork start-env` Enter
Hide
Wait+Screen@120s /started sf-feature/
Show
Sleep 3s
Ctrl+L

Hide
Type `U() { command stackfork --state-dir $T/state "$@" urls | awk '/^App:/{print $2}'; }; MAIN=$(U --root $T/code/acme-web); FEATURE=$(U); m() { printf 'main     '; curl -s $MAIN; echo; }; f() { printf 'feature  '; curl -s $FEATURE; echo; }; clear` Enter
Sleep 1s
Show
Type "# same code, separate databases, separate ports"
Enter
Sleep 600ms
Type `m; m; m; f` Enter
Sleep 4s
Type `stackfork list` Enter
Sleep 4s
Ctrl+L

Type "# done with a branch? tear it down, data too"
Enter
Sleep 600ms
Type `stackfork down --volumes && stackfork list` Enter
Hide
Wait+Screen@120s /BRANCH/
Show
Sleep 4s

Hide
Type `cd $T/code/acme-web; docs/screenshots/demo-world.sh reset >/dev/null 2>&1; echo cleaned-$((1+1))` Enter
Wait+Screen@240s /cleaned-2/
