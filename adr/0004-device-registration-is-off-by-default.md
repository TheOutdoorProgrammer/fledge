# Device registration is off by default

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

An ad hoc build installs only on devices named in its provisioning profile, so the recurring cost of running Fledge is collecting UDIDs.
Doing that by hand means a cable, a Mac, and Finder or Xcode, for every device, every time.

Apple's OTA Profile Service reads a UDID from a browser: a configuration profile with a `DeviceAttributes` array, and the device posts a signed plist back.
Making that self-service is the obvious move, and it is where the danger is.

Apple allows 100 devices of each type per membership year.
Disabling a device does not free its slot.
**Removing a device does not free its slot either.**
The count resets only when the membership year rolls over, through a one-time prompt on first sign-in to the developer portal, and that prompt disappears permanently the moment the first new device of the year is registered.

An unauthenticated registration page therefore lets anything that can reach it spend an annual allowance that cannot be recovered until renewal.

## Considered options

### Registration off unless a token is configured, gated on that token, and disclosing capacity

* Good, because the destructive default is opt-in rather than opt-out.
* Good, because the token is one environment variable, so switching it on is a deliberate act.
* Good, because showing slots used against the limit puts the irreversible cost in front of the person about to spend one.
* Good, because looking a UDID up before registering means a device already known is re-enabled rather than added, so repeat visits cost nothing.
* Bad, because a link with a token in it is weak authentication, and anyone it is forwarded to can register.
* Bad, because a token in a URL can end up in logs.

### Open registration

* Good, because it is one fewer thing to hand out.
* Bad, because the failure is permanent and silent. Nobody notices the allowance draining until a registration is refused.

### No registration, collect UDIDs by hand

* Good, because no slot is ever spent by accident.
* Bad, because it is the friction the tool exists to remove, and it needs a cable and a Mac for every device.

## Decision outcome

Registration stays off until `FLEDGE_ENROLL_TOKEN` is set.
The pages a person drives are gated on that token; the callback iOS posts to and the page it lands on afterwards are not, because iOS sends no token, and they are bound by a single-use challenge instead.

Fledge looks a UDID up before registering it, re-enables an existing disabled device rather than adding a new one, and states in the outcome whether a slot was actually consumed.
When App Store Connect credentials are configured the registration page shows how much of the annual allowance is gone.

Registering a device is the only write Fledge performs on a developer account without being asked, and it is unavoidable, because that is the point.
Every other write is offered rather than taken.
A device already registered under a different name than the one it reports is a real and common case, since a UDID added years ago by another tool keeps whatever label that tool gave it.
Fledge reports the mismatch and offers a button, authorised by the enrolment cookie so a browser can only rename the device it just enrolled.
It does not rename anything on its own: a page visit silently editing a developer account is a surprise, and the person looking at the page is the one who knows whether the old name meant something.

Two constraints found the hard way and worth not rediscovering.
The App Store Connect API key must hold the **Admin** role, because an Ad Hoc profile is a distribution profile and the Developer role cannot create one.
Provisioning profiles are immutable, with no update endpoint at all, so adding a device means creating a replacement profile and deleting the old one afterwards rather than editing it.

The remaining exposure is honest and unfixed: a token in a link is weak, and someone determined to spend the allowance who has the link can.
It is a homelab tool on a private network, and the alternative was either no registration at all or a login system nobody wants. If Fledge is ever exposed publicly, this decision needs revisiting and a real one written in its place.
