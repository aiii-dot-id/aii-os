# The files the released 0.1.22 program writes for a first install

Read by the tests of `config_strip.go`: what an identity an earlier release wrote whole meets at its first start of
this one.

These files were written by the program at the tag `v0.1.22`, and by
nothing later: that tree was exported, one command was added to it that calls `LoadConfig` and `loadProvidersFile`
on an empty directory (the files a first start writes), and it was run once. The command called only what that tree
already had.

| File | What it is |
|---|---|
| `config.json` | The configuration 0.1.22 writes for a first install: every default, as though typed. Not edited. |
| `providers.json` | The providers file 0.1.22 writes at its first use: the shipped registry, less what it lent. Not edited. |

A test copies these files into a temporary directory and never writes here. To write them again with another
released program, do as above with that program's tree, and copy the two files it wrote, unedited.
