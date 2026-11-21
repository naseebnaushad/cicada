# Unreleased changes

One change note per branch: `documents/unreleased/<branch with / replaced by ->.md`
(for example `feature-login-rework.md`). Claude writes it with `/change-note` at the end of
every task; commits and pushes are blocked without it.

`/promote beta` (or `release`) compiles every note here into the four Beta/Release
documents and moves them to `documents/vX.Y.Z/alpha/changes/`. Don't edit another branch's
note, and don't change `VERSION` or `documents/CHANGELOG.md` yourself.

Front matter: `type` (feature, fix, improvement, breaking, security, docs, internal),
`area`, `title`, `authors`, `branch`, `cr`, `after_the_fact`. Sections: What changed and why;
How it works; Tests added and results; Bugs fixed; Notes for users / upgrade steps;
Technical notes; Suggestions.
