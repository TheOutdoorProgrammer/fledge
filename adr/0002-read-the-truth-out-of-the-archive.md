# Read the truth out of the archive

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

Publishing a build needs a bundle identifier, a version, a title, an icon, and the manifest that iOS fetches.
Every comparable tool takes those as upload parameters or keeps them in a sidecar the operator maintains.

An ad hoc install also fails in ways nobody can see coming.
The device is not in the provisioning profile, the profile expired, the archive was signed for the App Store: iOS reports all three as an unexplained "Unable to Install", and none of them are visible in metadata an uploader typed by hand.

## Considered options

### Parse the archive and derive everything from it

An `.ipa` is a zip.
`Payload/<App>.app/Info.plist` carries the identifier, both version strings, the display name and the minimum OS.
`Payload/<App>.app/embedded.mobileprovision` is a CMS envelope wrapping a plist that carries the team, the expiry date, and the list of device identifiers the build will install on.

* Good, because the metadata cannot disagree with the binary. It came out of it.
* Good, because uploading is one request with no parameters, so the CLI and any other client stay trivial.
* Good, because the profile makes the invisible failures visible: expiry, device list, and distribution type are all readable before anyone taps Install.
* Good, because distribution type is derivable, so the server knows an App Store archive can never install over the air and can refuse rather than offer a link that does nothing.
* Bad, because it means parsing plists, CMS envelopes, and Apple's CgBI variant of PNG.
* Bad, because a future Xcode could change the layout inside the archive.

### Accept metadata as upload parameters

* Good, because the server stays a file store.
* Bad, because every caller has to extract the same values, so the parsing does not disappear, it just moves and gets duplicated.
* Bad, because nothing stops the metadata being wrong, and a manifest whose bundle identifier disagrees with the package is rejected by iOS with no useful message.
* Bad, because the profile's contents are the interesting part and no sane upload API asks a human to type a device list.

## Decision outcome

Read the archive.
The upload endpoint takes the archive as the entire request body and no parameters at all.

Two details cost more than expected and are worth recording.

Distribution type is not stamped anywhere in a profile.
It is derived: `ProvisionsAllDevices` means enterprise, no device list means App Store, and a device list plus the `get-task-allow` entitlement separates development from Ad Hoc, because that entitlement is what lets a debugger attach.

Icons in a built bundle are Apple's CgBI variant of PNG, which no browser renders.
The IDAT stream is raw deflate with no zlib wrapper, the channel order is BGRA, and colour is premultiplied by alpha.
Converting it is worth the roughly hundred lines it takes, because the OTA manifest requires both image assets and Xcode's own generator refuses to produce a manifest without them.
