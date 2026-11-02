# Third-party notices

NetMonitor is distributed under the MIT license. The following third-party assets and dependencies keep their own licenses and notices.

## Cisco topology icons

The SVG files in `frontend/src/assets/cisco/` are used only to render device-type icons on network maps:

- `cisco-router.svg`, `cisco-switch.svg`, `cisco-firewall.svg`, `cisco-server.svg`, `cisco-cpe.svg`: [Network-Icons-SVG](https://github.com/aci686/Network-Icons-SVG) (Cisco network topology icon set; the source states that the icons may be used freely but may not be altered).
- `cisco-wireless.svg`: [Cisco network device SVG library](https://www.nectus5.com/wp/svg/Cisco_Wireless_Access_Point_AP.svg).

The files are redistributed unmodified. Cisco and the Cisco logo are trademarks of Cisco Systems, Inc. They are used for network identification and diagramming only and do not imply sponsorship or endorsement by Cisco.

These icon files are **not** covered by the project's MIT license.

## Fonts

The frontend bundles the following fonts through `@fontsource` packages:

- **Inter** — Copyright (c) The Inter Project Authors. Licensed under the SIL Open Font License 1.1.
- **JetBrains Mono** — Copyright (c) JetBrains s.r.o. Licensed under the SIL Open Font License 1.1.

## Go and JavaScript dependencies

The backend dependencies are listed in `backend/go.mod` and `backend/go.sum`. The frontend dependencies are listed in `frontend/package.json` and `frontend/package-lock.json`. Each dependency retains its own license; refer to the upstream projects for the full text.

## Reporting

If you believe an asset is missing attribution or is included incorrectly, please open an issue in the repository.
