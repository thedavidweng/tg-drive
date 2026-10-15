# 0047: Native TypeScript compiler with the supported ESLint API

Status: Accepted.

Context: TypeScript 7.0.2 no longer ships the JavaScript compiler API.
typescript-eslint 8.71.0 therefore fails before linting any frontend file.
ADR 0035 retained TypeScript 6 until this toolchain could be upgraded together.

Decision: Use Microsoft's documented side-by-side installation: exact alias
`@typescript/native` points to `typescript@7.0.2`, providing `tsc`, while
`typescript` points to `@typescript/typescript6@6.0.2`, providing the API
required by ESLint. Keep all typecheck, lint, behaviour-test and build gates.

Consequences: The compiler and API have explicit, independently pinned roles;
there is no runtime retry or skipped lint. Revisit the API alias when
[typescript-eslint supports the native API](https://github.com/typescript-eslint/typescript-eslint/issues/10940).
The installation follows the [official migration instructions](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/#running-side-by-side-with-typescript-6.0).
