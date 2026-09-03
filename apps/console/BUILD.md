# Building the console

Requires Node 18+ (Node 20 recommended).

```
cd apps/console
npm install
npm run build      # outputs to apps/console/dist/
```

The Go server serves the built console from ALIMDM_CONSOLE_DIR (point it at
apps/console/dist). For a live dev server use Vite (npm run dev) which proxies /api to the Go server.

## Linting

```
npm run lint
```

`npm run build` runs the linter first and refuses to build if it fails. That is
deliberate: a component used without being imported, and a `const` read by an
effect's dependency array before its own declaration, both survived a clean
`vite build` and reached the server, where the first broke one page and the
second white-screened the console. Neither is visible to Vite, and both are
caught here.

`react-hooks/set-state-in-effect` is turned off in `eslint.config.js`, with the
reasoning next to it. ESLint is pinned to 9.x because eslint-plugin-react does
not accept 10 yet.
