# Records docs/demo.gif. Run from the repository root:
#   DEMO_TMP=$(mktemp -d) vhs docs/demo.tape
# Needs vhs, ttyd, ffmpeg, Go and Docker (images node:22-alpine and postgres:17).
# Everything runs in $DEMO_TMP with its own proxy (sf-demo-proxy on port 8099).
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1100
Set Height 640
Set Padding 16
Set Framerate 12
Set PlaybackSpeed 1.5
Set TypingSpeed 35ms

Hide
Type `export T=$DEMO_TMP STACKFORK_CONFIG_DIR=$DEMO_TMP/config STACKFORK_PROXY_NAME=sf-demo-proxy PS1='\W $ '` Enter
Type `mkdir -p $T/bin $T/config $T/state && go build -o $T/bin/stackfork ./cmd/stackfork && export PATH=$T/bin:$PATH` Enter
Type `stackfork() { if [ "$1" = list ]; then command stackfork --state-dir $T/state list | sed "s#$T/#.../#"; elif [ "$1" = prepare ]; then command stackfork --state-dir $T/state "$@" | sed "s#\"state\": .*#\"state\": \"...\",#"; else command stackfork --state-dir $T/state "$@"; fi; }` Enter
Type `mkdir $T/acme-web && cp examples/node-postgres/{Dockerfile,docker-compose.yml,package.json,server.js} $T/acme-web/ && cd $T/acme-web` Enter
Type `sed -i.bak 's#build: examples/node-postgres#build: .#' docker-compose.yml && rm docker-compose.yml.bak` Enter
Type `git init -q -b main && git add -A && git -c user.name=demo -c user.email=demo@example.com commit -qm init && git worktree add -q ../acme-web-feature-login -b feature-login` Enter
Type `stackfork proxy up --port 8099 >/dev/null && stackfork prepare -f docker-compose.yml >/dev/null && stackfork up >/dev/null 2>&1 && stackfork down >/dev/null 2>&1 && clear` Enter
Wait+Line@120s /acme-web \$ ?$/
Show

Type `stackfork prepare -f docker-compose.yml && stackfork up` Enter
Wait+Line@90s /acme-web \$ ?$/
Sleep 1s
Type `clear` Enter
Type `cd ../acme-web-feature-login` Enter
Type `stackfork prepare -f docker-compose.yml && stackfork up` Enter
Wait+Line@90s /feature-login \$ ?$/
Sleep 1s
Type `clear` Enter
Type `stackfork list` Enter
Sleep 2s
Type `curl -s -w '  %{url_effective}\n' $(stackfork urls | sed -n 's/^app: //p')` Enter
Sleep 2s
Type `cd ../acme-web && curl -s -w '  %{url_effective}\n' $(stackfork urls | sed -n 's/^app: //p')` Enter
Sleep 5s

Hide
Type `stackfork down --volumes >/dev/null 2>&1; cd ../acme-web-feature-login && stackfork down --volumes >/dev/null 2>&1; stackfork proxy down >/dev/null 2>&1; echo cleaned-$((1+1))` Enter
Wait+Screen@120s /cleaned-2/
