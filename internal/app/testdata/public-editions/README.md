# Historical public document fixtures

These exact public document bytes exercise upgrade recognition in
`internal/app/seed_editions_test.go`. Tests copy them into temporary directories;
they never modify this fixture directory. The recognition key normalizes the
`SKILLS.md` build stamp and otherwise hashes the original document bytes.
Keep the payloads and their keys unchanged: editing an old edition would no
longer test recognition of that edition.

The v0.1.21 fixtures are the unchanged documents at public commit
`ce2622bdfa3a470c894a629a182d3a04a434aafc`. The same upgrade test also
preserves genuine edits across two starts, leaving the current template beside
the edited document.

| File | Recognition key |
|---|---|
| `SKILLS-v0.1.12.md` | `9faf7f175725dbba40bf954e4e420527a40a4d13420f02aeb4cc8d442e89f312` |
| `SKILLS-c9d7197.md` | `1a50b4664d90871a976756cdfb079b82a6a033b4b90bce40c5f8944e259f3328` |
| `SKILLS-v0.1.14.md` | `f325a980481329ca1f3f231c6f8e61063fec6b9fe98485a2933f96ff29e6fd2e` |
| `SKILLS-86ead06.md` | `57241f25ad3a1c26b691a1f2f59690aa7baa3a2e8e94e91d1160294659dfdf26` |
| `SKILLS-v0.1.15.md` | `355b608af32ff10e6511a649a8af1e2cb21631bd6e804dda9de835c799cae53c` |
| `SKILLS-ffe0bfe.md` | `0ce14882bb82a6860bdaeee3f7939a8ef0d7575dabbc686f56b0d1076024e265` |
| `SKILLS-v0.1.16.md` | `2d1b391982f200c895bd5baaa5010371e79eec581a0143ce15d633bc93ff3e53` |
| `SKILLS-v0.1.21.md` | `22c042e7b2f61fe778e27ad66445686728ac9e1cc129f8e3bb366791ffe3f50d` |
| `METHOD-v0.1.12.md` | `b6e07e905b7aedccb013b0f8f485e1f157ec049d0cbe045818851658eef33cd0` |
| `overlay_README-v0.1.12.md` | `6c5c998f4c7d582d74d85926774ce315e8124221087ce40b0f913e4c74c7d180` |
| `overlay_README-v0.1.14.md` | `dcaa806c251ad13d0b385ee4f1723d2d304c6110ea1e74665f389c0d4b4b3311` |
| `overlay_README-v0.1.15.md` | `b6f814ed6f7dc4eb0e709f2864610fe4c177e5f569c509ea08b6ffb8486f3cf3` |
| `overlay_README-v0.1.21.md` | `b6665114b1afb827380241ff257c211e8d902a6004b1e6eb483b9b542e92f355` |
