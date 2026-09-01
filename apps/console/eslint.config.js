// Lint rules for the console.
//
// Two mistakes got past a clean `vite build` and into a deploy: a component
// used without being imported, which only failed once that page rendered, and
// a const read by an effect's dependency array before its own declaration,
// which white-screened the whole app. Neither is subtle — they are exactly what
// no-undef and no-use-before-define exist for. Hence this file.
//
// ESLint is pinned to 9.x because eslint-plugin-react does not yet accept 10.

import js from "@eslint/js";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";

export default [
  { ignores: ["dist/**", "node_modules/**"] },
  js.configs.recommended,
  {
    files: ["**/*.{js,jsx}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      parserOptions: { ecmaFeatures: { jsx: true } },
      globals: { ...globals.browser },
    },
    settings: { react: { version: "detect" } },
    plugins: { react, "react-hooks": reactHooks },
    rules: {
      ...react.configs.flat.recommended.rules,
      ...reactHooks.configs.recommended.rules,

      // The new JSX transform; React is not a required import.
      "react/react-in-jsx-scope": "off",
      // This codebase does not declare prop types, deliberately.
      "react/prop-types": "off",

      // The one that would have caught the white screen. Functions are exempt:
      // hoisted declarations called later are how this file is laid out.
      "no-use-before-define": ["error", { functions: false, variables: true }],
      "no-unused-vars": ["error", { argsIgnorePattern: "^_", caughtErrors: "none" }],

      // Off, deliberately. This fires on `useEffect(() => { load(); }, [load])`
      // — fetch on mount, then setState — which is how every page in this
      // console loads its data, and on resetting state when the route changes.
      // The rule belongs to the React Compiler's stricter model; adopting it
      // would mean rewriting seventeen call sites to silence advice about
      // cascading renders that cost nothing at this size. The rules that catch
      // real breakage are the ones above.
      "react-hooks/set-state-in-effect": "off",
    },
  },
];
