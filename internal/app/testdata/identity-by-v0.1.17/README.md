# Version 0.1.17 configuration fixture

`config.json` is the default first-install configuration written by version
0.1.17. The upgrade tests copy these exact bytes into a temporary directory and
exercise the current loader against them. The fixture contains no identity
ledger, conversation, signing key, or recorded user configuration.

To reproduce a default-configuration fixture, export the relevant released
source, call `LoadConfig` on a nonexistent configuration path in a temporary
directory, and retain the generated file unchanged. Record the producing
version and digest with the new fixture. Do not replace an old version's bytes.
