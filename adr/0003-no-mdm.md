# No MDM

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

Every ad hoc install needs the device's UDID in the provisioning profile, and every install needs someone to tap through a prompt.
Mobile device management appears to solve both: an MDM server can push an application to an enrolled device, and enrolment tells the server what the device is.

The question was whether Fledge should be, or embed, an MDM.

## Considered options

### Build or embed MDM

* Good, because MDM enrolment does hand the server a device's UDID, serial and IMEI.
* Good, because `InstallApplication` can push a build without anyone opening a page.
* Bad, because **MDM does not bypass code signing.** `InstallApplication` with a `ManifestURL` fetches the same manifest plist Fledge already serves, and the device still validates the embedded provisioning profile. The UDID must still be in it, and the hundred-device annual limit still applies. No reach is gained.
* Bad, because **silent installation requires a supervised device.** On an unsupervised personal iPhone the OS still shows an Install or Later prompt, which is the same single tap as following a link. Supervision means Apple Business Manager, or Apple Configurator over USB, which erases the device.
* Bad, because **the enrolment type that returns a UDID is the one requiring supervision.** User Enrollment, the BYOD path, deliberately withholds UDID, serial, IMEI and MAC, returning an opaque identifier that regenerates on re-enrolment.
* Bad, because running an MDM server needs an APNs push certificate whose signing request must be countersigned by an MDM vendor certificate. Apple issues those on a discretionary, manual basis aimed at vendors shipping an MDM product, and the community service that countersigns for others excludes individuals and personal devices by its own terms.
* Bad, because the protocol changes every year. Legacy software-update commands were removed in the 2026 releases, so self-hosting MDM is a standing maintenance commitment.

### Use an existing MDM alongside Fledge

Fleet is MIT licensed, self-hostable, supports iOS, and countersigns push certificate requests with its own vendor certificate, so a push certificate can be obtained with an ordinary Apple Account and no Business Manager.

* Good, because it removes the vendor certificate problem entirely.
* Good, because MDM is genuinely useful for its own sake: configuration profiles, network payloads, remote wipe, inventory.
* Bad, because none of that is distribution, and the three limitations above are unchanged by which MDM is running.

### Solve the UDID problem directly instead

Apple's OTA Profile Service reads a UDID from a browser without any of the MDM apparatus.

* Good, because it addresses the actual constraint, which is getting UDIDs into a provisioning profile.
* Good, because it needs no push certificate, no supervision, and no enrolment relationship with the device.
* Bad, because it is undocumented legacy surface, which [ADR 0004](0004-device-registration-is-off-by-default.md) covers.

## Decision outcome

Fledge is not an MDM and will not embed one.

The one thing MDM offers that Fledge cannot is unattended installation, and that requires supervising the device, which requires either Apple Business Manager or erasing the phone.
Neither is a reasonable price for removing one tap.

Anyone wanting MDM for its own sake should run Fleet next to Fledge.
Its ability to push an `.ipa` is a convenience over a link, not a capability Fledge lacks, and the UDID still has to be in the profile either way.

TestFlight deserves an honest mention as the alternative that deletes this whole problem: internal testers get builds in minutes without review and without consuming ad hoc device slots.
It is the right answer for anyone who does not specifically want the build to stay off Apple's infrastructure.
Fledge exists for the case where you do.
