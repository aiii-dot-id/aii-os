# Linux native plugin containment

The host refuses native plugin activation when it cannot establish containment.
When its diagnostics identify restricted unprivileged user namespaces as the
cause, an administrator can review this AppArmor profile for the installed
`/usr/bin/bwrap` executable. This changes security policy; review it for the host
before applying it. An unexplained failure is not proof that this remedy applies.

Save the profile as `/etc/apparmor.d/bwrap`:

```text
abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
    userns,
    include if exists <local/bwrap>
}
```

Load the reviewed profile with administrator privileges:

```sh
apparmor_parser -r /etc/apparmor.d/bwrap
```

Retry activation after applying the relevant fix. Do not remove containment to
make a failed probe appear to pass. The host's refusal and this profile are
checked together by the test suite.
