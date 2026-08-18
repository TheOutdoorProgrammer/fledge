# Split the build from the server

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

Fledge is meant to be driven by an agent: someone says "release this" and the whole path from source to an installable link runs unattended.
The obvious reading of that is a server that builds.

It cannot be.
`xcodebuild` runs only on macOS with Xcode installed, and code signing needs the developer's certificates and private keys in a keychain.
A distribution server is the thing most exposed on the network, and it is the last place those keys should live.

## Considered options

### A CLI that builds on the developer's Mac and uploads to a server that only distributes

* Good, because no signing certificate, private key, or Apple credential is needed to distribute a build, so the exposed service holds nothing worth stealing.
* Good, because the machine an agent already runs on is a Mac with Xcode, so nothing has to be dispatched anywhere.
* Good, because the server runs on Linux in a container and stays a plain HTTP application.
* Bad, because a release can only be cut from a Mac.
* Bad, because two binaries have to stay compatible with each other.

### A server that builds

* Good, because a release could be triggered from anywhere, including a browser.
* Bad, because it requires macOS, which rules out running it in the existing container platform.
* Bad, because signing keys would have to reach the server, which is the failure mode this design exists to avoid.

### A server that dispatches builds to a Mac build agent

* Good, because releases could be triggered from anywhere while signing stays on the Mac.
* Bad, because it is a job queue, an agent protocol, and an authentication boundary in service of a problem nobody has: the agent asking for the release is already on the Mac.
* Bad, because the dispatch channel becomes a remote code execution path into the machine holding the signing keys.

## Decision outcome

Ship two binaries.
`fledge` archives, exports and uploads, and runs wherever Xcode is.
`fledged` receives finished archives and serves them, and runs anywhere.

The server derives everything it displays from the uploaded archive rather than from what the uploader claims, so the two binaries share an artifact rather than a schema and cannot drift into disagreeing about a build.

A build dispatcher is not ruled out forever.
It becomes worth building the day a release needs to start somewhere that is not a Mac, such as a CI runner or a webhook, and not before.
