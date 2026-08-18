# Publish to Fledge

Uploads a signed `.ipa` to a Fledge server and gives you back an install link.

It is a composite action using nothing but `curl` and `jq`, so it runs on any GitHub runner, macOS or Linux, with nothing to install.

## Use it

```yaml
- uses: TheOutdoorProgrammer/fledge/actions/publish@v1
  id: fledge
  with:
    server: https://fledge.example
    token: ${{ secrets.FLEDGE_TOKEN }}
    ipa: build/MyApp.ipa

- run: echo "Install it at ${{ steps.fledge.outputs.page-url }}"
```

Everything else is read out of the archive, so there is no version or bundle identifier to keep in step with the build.

## A whole workflow

```yaml
name: beta

on:
  push:
    branches: [main]

jobs:
  beta:
    runs-on: macos-15
    steps:
      - uses: actions/checkout@v5

      - name: Import the signing certificate
        uses: apple-actions/import-codesign-certs@v5
        with:
          p12-file-base64: ${{ secrets.DISTRIBUTION_CERTIFICATE_P12 }}
          p12-password: ${{ secrets.DISTRIBUTION_CERTIFICATE_PASSWORD }}

      - name: Install the provisioning profile
        uses: apple-actions/download-provisioning-profiles@v4
        with:
          bundle-id: com.example.myapp
          profile-type: IOS_APP_ADHOC
          issuer-id: ${{ secrets.ASC_ISSUER_ID }}
          api-key-id: ${{ secrets.ASC_KEY_ID }}
          api-private-key: ${{ secrets.ASC_PRIVATE_KEY }}

      - name: Archive
        run: |
          xcodebuild archive \
            -project MyApp.xcodeproj -scheme MyApp \
            -destination 'generic/platform=iOS' \
            -archivePath build/MyApp.xcarchive

      - name: Export
        run: |
          xcodebuild -exportArchive \
            -archivePath build/MyApp.xcarchive \
            -exportOptionsPlist ExportOptions.plist \
            -exportPath build

      - uses: TheOutdoorProgrammer/fledge/actions/publish@v1
        with:
          server: https://fledge.example
          token: ${{ secrets.FLEDGE_TOKEN }}
          ipa: build/*.ipa
          fail-on-development-signing: true
```

## Inputs

| Input | Required | Default | |
| --- | --- | --- | --- |
| `server` | yes | | Base URL. Must be `https`, because iOS refuses to install over plain HTTP and reports nothing when it does |
| `token` | yes | | Upload token. Pass a secret |
| `ipa` | yes | | Path to the archive. A glob is allowed when it matches exactly one file |
| `notes` | no | the commit subject | A line shown on the install page |
| `fail-on-development-signing` | no | `false` | Fail the job when the archive is development signed |
| `summary` | no | `true` | Write the install link to the run summary |

## Outputs

`install-url`, `page-url`, `build-id`, `version`, `build`, `bundle-id`, `profile`, `expires`.

`page-url` is the one to share: it is the page a person opens on a device. `install-url` is the raw `itms-services://` manifest URL, which only works when followed from Safari.

## Why fail-on-development-signing exists

An archive exported with the `debugging` method is signed by a developer account, and iOS will not launch it until the tester enables Developer Mode and restarts the device.
Ad Hoc (`release-testing`) avoids that.
Every tester hitting that wall is worth one failed CI job instead, which is what this flag buys.

The action warns either way; the flag decides whether the warning stops the build.

## What it does not do

It does not build, sign, or register devices.
Use `apple-actions/import-codesign-certs` and `apple-actions/download-provisioning-profiles` for the signing material, and register devices through the Fledge server itself.
