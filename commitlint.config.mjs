// The list of commit types this repo accepts.
//
// Two other places must agree with it, or releases break quietly:
//   - .github/workflows/conventional-commits.yml — gates the PR title
//   - release-please-config.json (changelog-sections) — decides what gets released
//
// A type accepted here but hidden from release-please means merges that cut no
// release and no published binaries, with nothing to notice it. Change the list
// in all three places or not at all.
//
// ESM (.mjs), not .js: commitlint runs inside the action's own container, whose
// root package.json declares "type": "module". A CommonJS .js config fails to
// load there — and fails only in CI, not under a local commitlint run.
export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "type-enum": [
      2,
      "always",
      [
        "feat",
        "fix",
        "docs",
        "chore",
        "refactor",
        "perf",
        "test",
        "build",
        "ci",
        "revert",
        "style",
      ],
    ],
  },
};
