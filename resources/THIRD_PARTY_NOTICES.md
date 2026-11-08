# Third-party notices

Ferry's own source code is covered by the MIT License in the repository's
`LICENSE` file. Third-party components retain their respective licenses;
Ferry's MIT license does not replace them.

This file is installed with the application as
`resources\THIRD_PARTY_NOTICES.md`. The `resources\licenses` directory contains
the other notices listed below.

## Capture and proxy engine

### InterceptSuite / ProxyBridge 4.3.0

Ferry distributes the upstream Windows engine binaries
`resources\engine\win\amd64\ProxyBridgeSvc.exe` and `ProxyBridgeCore.dll`.
No modified engine or driver source is included in Ferry.

- Source: https://github.com/InterceptSuite/ProxyBridge/tree/v4.3.0_Windows
- Source commit: https://github.com/InterceptSuite/ProxyBridge/commit/8cb9dceeed582fcd7c2baed8d20eec34934732fc
- License: MIT, copyright (c) 2025 Anof-cyber.
- The full upstream license is preserved beside these binaries as
  `LICENSE.ProxyBridge.txt`; it is not relicensed under Ferry's copyright.

### WinDivert 2.2.2

The accompanying `WinDivert.dll` and `WinDivert64.sys` expose a version 2.2
product description. The build script at the upstream ProxyBridge revision
above names `C:\WinDivert-2.2.2-A` as its dependency.

WinDivert is separate from ProxyBridge and is **not** replaced by ProxyBridge's
MIT license. Ferry distributes the library and driver under the upstream
project's LGPL-3.0-or-later licensing option. The same upstream license also
offers GPL-2.0 as an alternative. Selecting the LGPL option does not impose
an option-upgrade requirement or the GPL on Ferry's own source code.

- Corresponding source: https://github.com/basil00/WinDivert/tree/v2.2.2
- Corresponding source archive: https://github.com/basil00/WinDivert/archive/refs/tags/v2.2.2.zip
- Upstream license and both alternative license texts:
  `resources\engine\win\amd64\LICENSE.WinDivert.txt`.
- Ferry does not provide modified WinDivert source. Recipients retain the
  permissions and obligations described by the full original notices.

## Application runtime and user interface

The application binary contains these dependencies. When actual package
versions change, their corresponding upstream notices must be reviewed again.

| Component | Version | License / retained notice |
| --- | --- | --- |
| Go standard library and runtime | Version selected by `go.mod` / CI | `licenses\Go-LICENSE.txt` |
| MyGo | v0.8.1 | MIT, `licenses\MyGo-LICENSE.txt` |
| ebitengine/purego | v0.11.1 | Apache-2.0, `licenses\PureGo-LICENSE.txt` |
| golang.org/x/net | v0.59.0 | `licenses\Golang-X-Net-LICENSE.txt` |
| golang.org/x/sys | v0.48.0 | `licenses\Golang-X-Sys-LICENSE.txt` |
| golang.org/x/text | v0.43.0 | `licenses\Golang-X-Text-LICENSE.txt` |
| React / React DOM / scheduler | v19.2.7 | MIT, `licenses\React-LICENSE.txt` |
| lucide-react | v1.9.0 | ISC, including any bundled icon notices, `licenses\Lucide-LICENSE.txt` |

Microsoft WebView2 Runtime is a system prerequisite, not redistributed by
Ferry. The installer detects an existing runtime and offers Microsoft's
official download page when it is absent. Build-only tools such as Vite,
TypeScript, the Go compiler and NSIS are not listed as runtime dependencies.
