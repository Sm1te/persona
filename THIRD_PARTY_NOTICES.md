# Third-party notices

## Password login WebAssembly client

`web/public/pake/client.wasm` uses `github.com/bytemare/opaque` v0.18.0 (MIT),
its transitive cryptographic dependencies (MIT and BSD), and the Go runtime (BSD).
The build collects their full license texts in a generated
`THIRD_PARTY_NOTICES.txt` under the web build's `pake` output, deployed beside the WASM client at
`/pake/THIRD_PARTY_NOTICES.txt`. The generated notices must accompany binary distributions.

## Brand marks

`apple/Persona/Resources/Assets.xcassets/Brands` and `apple/PersonaWatch/Assets.xcassets/Brands` hold other products' marks
(`web/src/views/inbox/lark.svg` is a copy of `brand-lark` for the web inbox, and
`android/core/designsystem/src/main/res/drawable/brand_lark.xml`, `brand_claude.xml`, `brand_openai.xml` and `brand_trae.xml`
the same marks as Android vector drawables),
shown next to the service Persona connects to. The marks are trademarks of their owners; using them here does not imply
endorsement. They are drawn as published: not recolored (the one-color OS marks take the distribution's color), not
redrawn.

| Asset | Mark | Source | Terms |
| --- | --- | --- | --- |
| `brand-claude` | Claude (Anthropic) | Simple Icons `claude.svg` (CC0 for the file) | Anthropic's brand guidelines |
| `brand-openai` | OpenAI Blossom | github.com/openai/openai-cookbook, `chatgpt-blossom-black.svg` (viewBox cropped to the mark) | openai.com/brand |
| `brand-trae` | TRAE, the agent mark on iOS / Mac and Android (#6390) | Simple Icons `trae.svg` (CC0 for the file; viewBox cropped to the mark); upstream trae.ai | TRAE's trademark |
| `brand-lark` | 飞书 / Lark | github.com/larksuite/lark-samples, `public/svg/icon.svg` (viewBox cropped to the mark) | Lark user agreement; no public logo guidelines found |
| `brand-google` | Google G | developers.google.com/identity/branding-guidelines, `signin-assets.zip` (the G matted from the light and dark buttons) | Google Sign-In branding guidelines |
| `brand-gmail` | Gmail | gstatic.com Google product icon `gmail_2020q4_512dp.png` | Google brand terms |
| `brand-github` | GitHub mark | github.com/primer/octicons `mark-github` (MIT for the file) | GitHub's logo usage terms |
| `brand-microsoft` | Microsoft logo | The four-square logo of Microsoft's sign-in branding guidelines (learn.microsoft.com, Microsoft identity platform), written as four squares in its colors | Microsoft Trademark and Brand Guidelines |
| `brand-telegram` | Telegram | telegram.org/img/t_logo.svg | Telegram's trademark |
| `ios-app-*` | Apple 通讯录、日历、提醒事项、健康、照片、地图 app icons | App Store artwork of each app (iTunes lookup `artworkUrl512`, fetched at 240×240), shown next to the data Persona reads from that app | Apple trademarks; Apple's terms for its marks |
| `web/src/ui/brands/claude.svg` | Claude (Anthropic), agent mark in the web client | Same file as `brand-claude` | Anthropic's brand guidelines |
| `web/src/ui/brands/codex.svg` | OpenAI Blossom, the Codex agent mark in the web client | Same file as `brand-openai` | openai.com/brand |
| `web/src/ui/brands/opencode.svg` | OpenCode, agent mark in the web client | Simple Icons 16.36.0 `opencode.svg` (CC0 for the file; viewBox cropped to the mark); upstream github.com/anomalyco/opencode `packages/identity/mark.svg` (the project is MIT) | OpenCode's trademark |
| `web/src/ui/brands/cursor.svg` | Cursor, agent mark in the web client | Simple Icons 16.36.0 `cursor.svg` (CC0 for the file; viewBox cropped to the mark); upstream cursor.com/brand | Cursor's brand page |
| `web/src/ui/brands/gemini.svg` | Google Gemini, agent mark in the web client | Simple Icons 16.36.0 `googlegemini.svg` (CC0 for the file); upstream gemini.google.com, no brand kit found | Google brand terms |
| `web/src/ui/brands/pi.svg` | Pi, agent mark in the web client | Simple Icons 16.36.0 `pi.svg` (CC0 for the file); upstream pi.dev/favicon.svg, no licence text found | Pi's trademark |
| `web/src/ui/brands/trae.svg` | TRAE, agent mark in the web client | Same file as `brand-trae` | TRAE's trademark |
| `web/src/ui/brands/github.svg` | GitHub mark in the web client | Same file as `brand-github` | GitHub's logo usage terms |
| `web/src/ui/brands/telegram.svg` | Telegram in the web client | Same file as `brand-telegram` | Telegram's trademark |
| `os-*` | Ubuntu, Debian, Fedora, Red Hat, CentOS, Rocky Linux, AlmaLinux, Arch Linux, Alpine Linux, openSUSE, NixOS, Linux (Tux) | Simple Icons (CC0 for the files) | Debian CC-BY-SA-3.0, NixOS CC-BY-4.0, Rocky Linux CC-BY-SA-4.0; the others under each project's trademark policy |

## Web client

The web client's build writes `third-party-licenses.txt` next to its `index.html` (the hub serves it at
`/third-party-licenses.txt`). It lists every npm package `web/package-lock.json` installs for the client, which is every
package the build can include (xterm, pdf.js, noVNC, KaTeX, Mermaid and its dependencies, SheetJS and the rest), with its
version, the license it is used under, its source and the license text it is published with. `web/third-party-licenses.ts`
writes it, and the build fails (as does `web/third-party-licenses.test.ts` in CI's web tests) when a dependency is under a
license not accepted yet, offers a choice of licenses not made yet, no longer offers the license chosen, or comes without
a license file.

- Choices among offered licenses: DOMPurify (MPL-2.0 or Apache-2.0) is used under Apache-2.0, elkjs (EPL-2.0 or
  GPL-3.0-or-later) under EPL-2.0, JSZip (MIT or GPL-3.0-or-later) under MIT.
- Source of the copyleft code, bundled from the npm releases without changes to it: noVNC (MPL-2.0) at
  https://github.com/novnc/noVNC, elkjs (EPL-2.0) at https://github.com/kieler/elkjs, at the versions
  `third-party-licenses.txt` names.
- khroma's package.json names no license; its LICENSE file grants MIT. isarray, rehype-katex and remark-math (MIT) are
  published without a license file; their entries point at their source.
- Authors' email addresses are left out of the license texts: the published hub binary embeds the web client, and the
  release scan (`scripts/release/check_bundle_privacy.py`) refuses email addresses in it.

## Ovis

Parts of `server/internal/screen` (Claude Code input-box rules) are ported from Ovis, and
`server/internal/screen/testdata/claude-rewind-2.1.304-*` is taken from its screen fixtures.

https://github.com/devswha/ovis

MIT License

Copyright (c) 2026 devswha

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit
persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## JetBrains Mono

The terminal font (`apple/Persona/Resources/Fonts/JetBrainsMono-*.ttf`, v2.304, unmodified). Android carries it too
(`android/feature/terminal/src/main/res/font/jetbrains_mono.ttf`, the same file as the regular weight, and
`android/core/designsystem/src/main/res/font/jetbrains_mono.ttf`), and the website its Latin subset as WOFF2
(`site/public/fonts/jetbrains-mono-latin-400-normal.woff2`, `site/public/fonts/jetbrains-mono-latin-600-normal.woff2`);
each keeps the license beside it (`android/feature/terminal/OFL.txt`,
`android/core/designsystem/src/main/assets/licenses/OFL-jetbrainsmono.txt`, `site/public/fonts/OFL-JetBrainsMono.txt`).

https://github.com/JetBrains/JetBrainsMono

Copyright 2020 The JetBrains Mono Project Authors (https://github.com/JetBrains/JetBrainsMono)

This Font Software is licensed under the SIL Open Font License, Version 1.1.
This license is copied below, and is also available with a FAQ at:
https://scripts.sil.org/OFL


-----------------------------------------------------------
SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007
-----------------------------------------------------------

PREAMBLE
The goals of the Open Font License (OFL) are to stimulate worldwide
development of collaborative font projects, to support the font creation
efforts of academic and linguistic communities, and to provide a free and
open framework in which fonts may be shared and improved in partnership
with others.

The OFL allows the licensed fonts to be used, studied, modified and
redistributed freely as long as they are not sold by themselves. The
fonts, including any derivative works, can be bundled, embedded, 
redistributed and/or sold with any software provided that any reserved
names are not used by derivative works. The fonts and derivatives,
however, cannot be released under any other type of license. The
requirement for fonts to remain under this license does not apply
to any document created using the fonts or their derivatives.

DEFINITIONS
"Font Software" refers to the set of files released by the Copyright
Holder(s) under this license and clearly marked as such. This may
include source files, build scripts and documentation.

"Reserved Font Name" refers to any names specified as such after the
copyright statement(s).

"Original Version" refers to the collection of Font Software components as
distributed by the Copyright Holder(s).

"Modified Version" refers to any derivative made by adding to, deleting,
or substituting -- in part or in whole -- any of the components of the
Original Version, by changing formats or by porting the Font Software to a
new environment.

"Author" refers to any designer, engineer, programmer, technical
writer or other person who contributed to the Font Software.

PERMISSION & CONDITIONS
Permission is hereby granted, free of charge, to any person obtaining
a copy of the Font Software, to use, study, copy, merge, embed, modify,
redistribute, and sell modified and unmodified copies of the Font
Software, subject to the following conditions:

1) Neither the Font Software nor any of its individual components,
in Original or Modified Versions, may be sold by itself.

2) Original or Modified Versions of the Font Software may be bundled,
redistributed and/or sold with any software, provided that each copy
contains the above copyright notice and this license. These can be
included either as stand-alone text files, human-readable headers or
in the appropriate machine-readable metadata fields within text or
binary files as long as those fields can be easily viewed by the user.

3) No Modified Version of the Font Software may use the Reserved Font
Name(s) unless explicit written permission is granted by the corresponding
Copyright Holder. This restriction only applies to the primary font name as
presented to the users.

4) The name(s) of the Copyright Holder(s) or the Author(s) of the Font
Software shall not be used to promote, endorse or advertise any
Modified Version, except to acknowledge the contribution(s) of the
Copyright Holder(s) and the Author(s) or with their explicit written
permission.

5) The Font Software, modified or unmodified, in part or in whole,
must be distributed entirely under this license, and must not be
distributed under any other license. The requirement for fonts to
remain under this license does not apply to any document created
using the Font Software.

TERMINATION
This license becomes null and void if any of the above conditions are
not met.

DISCLAIMER
THE FONT SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO ANY WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT
OF COPYRIGHT, PATENT, TRADEMARK, OR OTHER RIGHT. IN NO EVENT SHALL THE
COPYRIGHT HOLDER BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
INCLUDING ANY GENERAL, SPECIAL, INDIRECT, INCIDENTAL, OR CONSEQUENTIAL
DAMAGES, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF THE USE OR INABILITY TO USE THE FONT SOFTWARE OR FROM
OTHER DEALINGS IN THE FONT SOFTWARE.

## Barlow, Manrope, Sora, Unbounded and Noto Serif CJK

Fonts under the same SIL Open Font License 1.1 as JetBrains Mono (its full text is above). Each keeps its license file
beside it.

| Font | Files | Copyright | License file |
| --- | --- | --- | --- |
| Barlow Semi Condensed | `android/core/designsystem/src/main/res/font/barlow_sc_regular.ttf`, `barlow_sc_medium.ttf`, `barlow_sc_semibold.ttf` | Copyright 2017 The Barlow Project Authors (https://github.com/jpt/barlow) | `android/core/designsystem/src/main/assets/licenses/OFL-barlow.txt` |
| Manrope | `android/core/designsystem/src/main/res/font/manrope.ttf` | Copyright 2018 The Manrope Project Authors (https://github.com/googlefonts/manrope) | `android/core/designsystem/src/main/assets/licenses/OFL-manrope.txt` |
| Sora | `android/core/designsystem/src/main/res/font/sora.ttf` | Copyright 2019 The Sora Project Authors (https://github.com/sora-xor/sora-font) | `android/core/designsystem/src/main/assets/licenses/OFL-sora.txt` |
| Unbounded (the website) | `site/public/fonts/unbounded-latin-800-normal.woff2` | Copyright 2022 The Unbounded Project Authors (https://github.com/googlefonts/unbounded) | `site/public/fonts/OFL-Unbounded.txt` |
| Noto Serif CJK SC Bold (the website's headlines, a subset made by `scripts/assets/make_site_assets.py`) | `site/public/fonts/persona-serif.woff` | Copyright 2017-2022 Adobe (http://www.adobe.com/), with Reserved Font Name 'Source' (https://github.com/notofonts/noto-cjk) | `site/public/fonts/OFL-NotoSerifCJK.txt` |

## KaTeX, Mermaid and Phosphor Icons

The apps draw formulas and diagrams offline with copies of KaTeX 0.16.22 (`katex.min.js`, `katex.min.css`) and Mermaid
12.3.0 (`mermaid.min.js`) in `apple/Persona/Resources/Render` and `android/core/designsystem/src/main/assets/render`
(the same files), each with its license beside it (`KaTeX-LICENSE.txt`, `mermaid-LICENSE.txt`). The web client takes
the npm packages instead (see "Web client").

`mermaid.min.js` is Mermaid's own bundle and carries its dependencies, among them DOMPurify, used under Apache-2.0, and
elkjs, used under EPL-2.0 (source at https://github.com/kieler/elkjs). The web client's generated notices list the same
packages with their license texts.

The Android app's icons (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/icons/PhIcons.kt`) are
generated from Phosphor Icons by `scripts/assets/gen_phosphor_icons.py`; the license is in
`android/core/designsystem/src/main/assets/licenses/MIT-phosphor-icons.txt`.

| Component | Source | Copyright |
| --- | --- | --- |
| KaTeX | https://github.com/KaTeX/KaTeX | Copyright (c) 2013-2020 Khan Academy and other contributors |
| Mermaid | https://github.com/mermaid-js/mermaid | Copyright (c) 2014 - 2022 Knut Sveidqvist |
| Phosphor Icons | https://github.com/phosphor-icons/core | Copyright (c) 2023 Phosphor Icons |

All three under the MIT License:

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit
persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## BeautifulUI

`apple/Packages/BeautifulUI` (the chat's thoughts and tool chips) is BeautifulUI, a SwiftUI port of Beautiful UI
(https://www.beautifului.dev/), copied in as a local Swift package (`apple/Packages/BeautifulUI/VENDORED.md`).
The package directory keeps both licenses (`LICENSE`, `LICENSE-beautifului`) and `NOTICE`.

The web client (`web/`) uses Beautiful UI's design tokens, keyframes and component grammar directly (`web/src/styles.css`,
the views under `web/src/`), reimplemented in React against Persona's data; Beautiful UI's license is the one in
`apple/Packages/BeautifulUI/LICENSE-beautifului` (MIT, Copyright (c) 2026 Shane Levine).

BeautifulUI — MIT License, Copyright (c) 2026 ChrisPan

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit
persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

Beautiful UI — MIT License, Copyright (c) 2026 Shane Levine

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit
persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## SwiftNIO SSH and its dependencies

The iOS and macOS apps link `apple/Packages/PersonaSSH` (the SSH tunnel to the hub, docs/operations/network.md), which depends on
the packages below. `Glue` and `ChannelDataUnwrapper` in `apple/Packages/PersonaSSH/Sources/PersonaSSH/Handlers.swift`
follow the `GlueHandler` and `SSHWrapperHandler` of swift-nio-ssh's `NIOSSHClient` example.

| Package | Source | License |
| --- | --- | --- |
| swift-nio-ssh | https://github.com/apple/swift-nio-ssh | Apache License 2.0 |
| swift-nio | https://github.com/apple/swift-nio | Apache License 2.0 |
| swift-nio-transport-services | https://github.com/apple/swift-nio-transport-services | Apache License 2.0 |
| swift-crypto | https://github.com/apple/swift-crypto | Apache License 2.0 |
| swift-atomics | https://github.com/apple/swift-atomics | Apache License 2.0 |
| swift-collections | https://github.com/apple/swift-collections | Apache License 2.0 |
| swift-system | https://github.com/apple/swift-system | Apache License 2.0 |
| swift-asn1 | https://github.com/apple/swift-asn1 | Apache License 2.0 |

Copyright (c) Apple Inc. and the SwiftNIO / Swift project authors. Licensed under the Apache License, Version 2.0
(https://www.apache.org/licenses/LICENSE-2.0); each package's LICENSE.txt and NOTICE.txt apply.

## JSch

The Android app's SSH tunnel to the hub (`android/core/hub/src/main/kotlin/persona/core/hub/tunnel/`,
docs/operations/network.md) uses JSch (https://github.com/mwiede/jsch, `com.github.mwiede:jsch`), which carries JZlib
(`com.jcraft.jsch.jzlib`) and jBCrypt (`com.jcraft.jsch.jbcrypt`) inside it. Authors' email addresses are left out of
the license texts.

JSch:

```text
Copyright (c) 2002-2015 Atsuhiko Yamanaka, JCraft,Inc.
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

  1. Redistributions of source code must retain the above copyright notice,
     this list of conditions and the following disclaimer.

  2. Redistributions in binary form must reproduce the above copyright
     notice, this list of conditions and the following disclaimer in
     the documentation and/or other materials provided with the distribution.

  3. The names of the authors may not be used to endorse or promote products
     derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED ``AS IS'' AND ANY EXPRESSED OR IMPLIED WARRANTIES,
INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL JCRAFT,
INC. OR ANY CONTRIBUTORS TO THIS SOFTWARE BE LIABLE FOR ANY DIRECT, INDIRECT,
INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA,
OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE,
EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

JZlib:

```text
Copyright (c) 2000-2011 ymnk, JCraft,Inc. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

  1. Redistributions of source code must retain the above copyright notice,
     this list of conditions and the following disclaimer.

  2. Redistributions in binary form must reproduce the above copyright
     notice, this list of conditions and the following disclaimer in
     the documentation and/or other materials provided with the distribution.

  3. The names of the authors may not be used to endorse or promote products
     derived from this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED ``AS IS'' AND ANY EXPRESSED OR IMPLIED WARRANTIES,
INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL JCRAFT,
INC. OR ANY CONTRIBUTORS TO THIS SOFTWARE BE LIABLE FOR ANY DIRECT, INDIRECT,
INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA,
OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE,
EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

jBCrypt:

```text
Copyright (c) 2006 Damien Miller

Permission to use, copy, modify, and distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

## MarkdownUI and its dependencies

The iOS, Mac and Vision apps link swift-markdown-ui (`apple/project.yml`) to render Markdown, which depends on the
packages below; its swift-snapshot-testing dependency is only for its own tests and is not linked.

| Package | Source | License |
| --- | --- | --- |
| swift-markdown-ui | https://github.com/gonzalezreal/swift-markdown-ui | MIT, Copyright (c) 2020 Guillermo Gonzalez |
| NetworkImage | https://github.com/gonzalezreal/NetworkImage | MIT, Copyright (c) 2020 Guille Gonzalez |
| swift-cmark (cmark-gfm) | https://github.com/swiftlang/swift-cmark | BSD-2-Clause, Copyright (c) 2014, John MacFarlane; parts under MIT: houdini (Copyright (C) 2012 Vicent Martí), buffer and chunk ((C) 2012 Github, Inc.), utf8 ((C) 2009 Public Software Group e. V., Berlin, Germany) |

Each package's LICENSE (swift-cmark's COPYING) applies.

## MarqueeGlow

The iOS app links MarqueeGlow (`apple/project.yml`) to draw the composer's glow while a reply is generating
(`apple/Persona/DesignSystem/ComposerGlow.swift`). It has no dependencies.

| Package | Source | License |
| --- | --- | --- |
| MarqueeGlow | https://github.com/szupzj18/MarqueeGlow | MIT, Copyright (c) 2026 ChrisPan |

The package's LICENSE applies.

## SheetJS

The web client opens spreadsheets with SheetJS Community Edition 0.20.3, kept as the tarball SheetJS publishes on
cdn.sheetjs.com (`web/vendor/xlsx-0.20.3.tgz`, docs/web/web.md) and listed in the web client's generated notices with its
license text.

https://sheetjs.com/

Copyright (C) 2012-present SheetJS LLC

Licensed under the Apache License, Version 2.0 (the "License"); you may not use these files except in compliance with
the License. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0. Unless required by
applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language
governing permissions and limitations under the License.

## App Store Connect CLI

`scripts/release/asc_cli.py` downloads a pinned release of the App Store Connect CLI (`asc`) from its GitHub releases
and runs it; the binary is not part of this repository or of anything Persona ships.

https://github.com/rorkai/App-Store-Connect-CLI

MIT License

Copyright (c) 2026 Rudrank Riyam

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit
persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## Lucide

The plan card's step marks (`web/src/views/chat/PlanCard.tsx`: circle, circle-check and the in-progress arc) and several
web icons drawn in `web/src/ui/icons.tsx` (sparkles, corner-down-right, workflow and others its comments name) take their
path data from Lucide, the icon set cindy uses.

https://github.com/lucide-icons/lucide

ISC License

Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as part of Feather (MIT). All other copyright (c) for Lucide are held by Lucide Contributors 2022.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

## cindy

`server/internal/assistant/models_catalog.json` (the built-in model catalog that fills in what a gateway lists) is taken from
cindy's model registry (`packages/model-providers/catalog/model-registry.json`): for the Claude series and the GLM,
Kimi, DeepSeek V4, Qwen3, MiniMax, Seed and MiMo models cindy runs on Claude Code, only the id, name, context window,
thinking levels (low, medium, high, xhigh, max) and default level, as the registry states them, reformatted to a flat
list. The provider settings (the add-a-provider steps, the connection test and the model list it fetches) and the web
chat's subagent and workflow cards (`web/src/views/chat/TaskCard.tsx`, after cindy's `AgentTaskCard`: layout, states and
wording) and the main chat's hand-off cards with the way-back strip on an Agent's page (`web/src/views/assistant/Handoff.tsx`,
`web/src/views/chat/HandoffBack.tsx`, after cindy's `SessionHandoffCard` and its return pill: layout, states and wording)
and a lead's Team side panel (`web/src/views/chat/TeamPanel.tsx`, after cindy's Team tab, `OrcaWorkerPanel` and its worker
strip: layout, states, wording and when it opens) follow cindy's design. No code is copied for those.

The web chat's work summary follows cindy's chat at the owner's request (2026-11-12): the timeline inside an opened
summary (`web/src/lib/work-groups.ts`, after `createCompletedWorkGroup` in `messageWorkGroups.ts`), the thought rows
(`web/src/views/chat/ThinkingRow.tsx`, after `ThinkingActivityRow` in `WorkGroupBlock.tsx`), the tool rows
(`web/src/views/chat/ActionRow.tsx`, after `AgentActionRow` and `activityRowChrome.ts`) and the durations (`workDuration`,
after `formatDuration`). Two files are ported from cindy's code with changes: `web/src/lib/thinking-text.ts` (from
`packages/maker-shared/src/thinkingText.ts`) and `web/src/views/chat/ThinkingText.tsx` (from
`apps/desktop/src/renderer/components/chat/ThinkingText.tsx`), with tests from cindy's.

The web chat's plan (`web/src/views/chat/PlanCard.tsx`, #5156) is ported from cindy's `TodoListCard.tsx` with changes:
the inline card that opens in place (`InlinePlanCard`), the pill above the composer with its hover / pinned flyout and
Close Plan (`TodoListCard`), the progress ring's arithmetic and the in-progress arc's path and sizes.

https://github.com/makecindy/cindy

Licensed under the Apache License, Version 2.0 (the "License"); you may not use these files except in compliance with
the License. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0. Unless required by
applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific language
governing permissions and limitations under the License.
