# Workload identity for CI publishing

* Status: accepted
* Date: 2026-08-18

## Context and problem statement

Publishing from CI means CI needs to authenticate.
The obvious answer is a shared token in each repository's secrets, and it has two problems that only show up later.

It is long lived, so it exists to be leaked, and rotating it means touching every repository that holds a copy.
It is also unscoped: any repository holding the token can publish under any bundle identifier, so one compromised workflow can replace a different app with something else.

## Considered options

### A GitHub workload identity token, authorised on the repository claim

GitHub Actions will mint a short-lived OIDC token for a job that asks for one.
Fledge verifies it against GitHub's published keys and reads the `repository` claim.

* Good, because nothing long lived is stored anywhere. The token is minted per run and expires in minutes.
* Good, because there is nothing to rotate, and nothing to leak from a repository's settings.
* Good, because authorisation is per repository *and* per bundle identifier, so a compromised workflow cannot publish someone else's app.
* Good, because a ref constraint can pin publishing to a branch.
* Bad, because it only works on a provider that issues these tokens.
* Bad, because the server now depends on reaching the issuer's discovery endpoint at startup.

### A shared token in every repository

* Good, because it works anywhere and needs no verification code.
* Bad, because it is long lived, copied widely, and unscoped.

### A token per repository

* Good, because a leak is contained to one repository and scoping becomes possible.
* Bad, because it is a credential lifecycle: issuing, storing, rotating and revoking, all by hand, forever.

## Decision outcome

Accept both. A workload identity token when the caller has one, the shared token otherwise.

The shared token stays unrestricted, because it is what `fledge release` uses from a Mac, where the person running it already has every credential involved.
A workload token is restricted to what its policy names.

Two details cost more thought than expected.

**Authorisation happens after the archive is parsed**, not in the middleware, because the bundle identifier is only knowable once the archive has been read. Authentication still happens first; an upload that then fails authorisation is deleted rather than left on disk.

**A ref constraint is not optional for anything sensitive.** A pull request from a fork runs with the same `repository` claim as the branch, so a policy naming only the repository lets a stranger's pull request publish. Naming `@refs/heads/main` is the difference, and it is the kind of hole that looks like it is closed when it is not.

The audience defaults to the server's own public URL rather than to anything, so a token minted for a different service cannot be replayed here.
