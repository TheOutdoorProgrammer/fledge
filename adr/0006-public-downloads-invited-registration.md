# Public downloads, invited registration

* Status: accepted
* Date: 2026-08-18
* Supersedes: [ADR 0004](0004-device-registration-is-off-by-default.md)

## Context and problem statement

ADR 0004 assumed a LAN-only server, gated registration on one shared token, and closed by saying that exposing Fledge publicly would need a new decision written in its place.
That happened: builds now need to reach devices that are not on the network, and CI needs to publish.

Reaching for accounts was the obvious move and the wrong one.
The question worth asking first is which surfaces actually need protecting, and the answer is narrower than it looks.

The binding constraint is that **the iOS installer cannot authenticate.**
When someone taps Install, the manifest and the package are fetched by the operating system, not the browser: no cookie, no redirect to a sign-in page, no bearer token.
Any session-based gate in front of those two routes breaks installation entirely, and iOS reports it as nothing at all.

## Considered options

### Public downloads, invitations for registration, a token for publishing

Three surfaces, three answers.
Browsing, install pages, manifests, packages and the version check are open.
Registering a device needs a single-use invitation.
Publishing needs the shared token or a workload identity.

* Good, because the surface that cannot authenticate is the surface that does not have to.
* Good, because it protects the thing that is actually irreversible. A download costs nothing; a registration permanently spends one of a hundred annual Apple slots.
* Good, because an ad hoc archive is UDID locked, so a stranger who downloads one cannot run it. The exposure is that the binary can be inspected.
* Good, because there are no accounts, passwords, sessions or resets to build, and nothing for a person to forget.
* Bad, because unreleased binaries are downloadable by anyone with a URL.
* Bad, because an invitation link is bearer authority: whoever holds it before it is redeemed can spend a slot.

### Accounts, with sign-in

* Good, because access could be revoked per person rather than per link.
* Bad, because it does not work. The install and manifest routes still cannot participate, so they need capability URLs regardless, and the accounts only protect what was already cheap to protect.
* Bad, because it is a login system, a reset flow and a session store in service of hiding downloads that are inert without a matching UDID.

### An identity provider in front of everything

* Good, because it is somebody else's code.
* Bad, because it breaks installation for the same reason, and the exceptions needed to unbreak it reintroduce the open routes.

## Decision outcome

Downloads are public. Registration needs an invitation. Publishing needs a token.

Invitations are single use, expire in a week by default, carry a note so a list is readable later, and can be revoked.
A permit is spent when a device actually registers, not when the page is opened, so a link that is read and ignored is not wasted.
Spent invitations stop advertising their link, so a used one cannot be handed out again by mistake.

`FLEDGE_ENROLL_TOKEN` survives as a standing invitation that never expires.
It suits a server with one operator and is deliberately not the default.

The exposure being accepted, stated plainly so nobody has to infer it: anyone who learns a URL can download an unreleased build and read its contents.
That is tolerable because the archive will not run for them, and because the alternative protected nothing that mattered while breaking the thing that did.
If Fledge is ever used for something where the binary itself is the secret, this needs revisiting again.
