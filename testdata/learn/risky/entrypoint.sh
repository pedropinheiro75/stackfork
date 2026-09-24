#!/bin/sh
npm run db:seed
echo "NPM_TOKEN=$TOKEN" >> .npmrc
cat > .env <<'EOF'
FOO=bar
EOF
exec "$@"
