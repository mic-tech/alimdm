# Building the console

Requires Node 18+ (Node 20 recommended).

```
cd apps/console
npm install
npm run build      # outputs to apps/console/dist/
```

The Go server serves the built console from FK_CONSOLE_DIR (point it at
apps/console/dist). For a live dev server use Vite (npm run dev) which proxies /api to the Go server.
