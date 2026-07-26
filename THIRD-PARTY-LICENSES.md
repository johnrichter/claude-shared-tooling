# Third-Party Licenses

This file lists the third-party code bundled into this repository's distributed signed binary and reproduces the license text each component requires.

Scope:

- Python side: zero third-party runtime dependencies (stdlib only).
- Go side (`go/build-helpers`): one third-party dependency, `doublestar` (glob matching in `bh/surface.go`), statically linked into the distributed `go/.bin/build-helpers-*` binaries.
- Go side (`go/logkit`): `zerolog` (the JSON stream's byte writer) and `jcs` (RFC 8785 canonicalization), plus zerolog's own `go-isatty`, `go-colorable` and `golang.org/x/sys` dependencies, statically linked into any binary that imports `go/logkit`.
- All other third-party code is the 35 Rust crates below, statically linked into the distributed binary.

The MIT, BSD-3-Clause, Zlib, Apache-2.0, and Unicode-3.0 licenses relied on below each require their copyright notice and permission/license text to travel with any binary that includes the licensed code. This file reproduces that attribution and license text for the statically-linked components, satisfying those obligations.

## License election

Several crates offer a choice of license via an SPDX `OR` expression (e.g. `MIT OR Apache-2.0`). For each such crate we elect one license to rely on; an SPDX `AND` term is not a choice — it is a mandatory additional obligation on top of whichever `OR` branch we elect.

Election rule applied: **elect MIT wherever MIT is offered** in an `OR` expression.

Working through all 35 crates under this rule, the licenses we actually rely on reduce to exactly four: MIT, BSD-3-Clause, Zlib and Unicode-3.0.

| License | How it applies |
| --- | --- |
| MIT | Elected for every crate whose SPDX expression offers it — the large majority; also `zerolog`, `go-isatty` and `go-colorable` on the Go side, each MIT with no `OR` alternative. |
| BSD-3-Clause | Mandatory `AND` term on `encoding_rs` = `(Apache-2.0 OR MIT) AND BSD-3-Clause`. We elect MIT for the `OR`; BSD-3-Clause still applies. Also `golang.org/x/sys`, BSD-3-Clause with no `OR` alternative — a different copyright holder and text from the `encoding_rs` one, kept as its own section below. |
| Zlib | `foldhash` = `Zlib` with no `OR` alternative — no election, Zlib is the only license. |
| Unicode-3.0 | Mandatory `AND` term on `unicode-ident` = `(MIT OR Apache-2.0) AND Unicode-3.0`. We elect MIT for the `OR`; Unicode-3.0 still applies. |
| Apache-2.0 | `jcs` (Go side) = `Apache-2.0` with no `OR` alternative — no election, Apache-2.0 is the only license. |

Apache-2.0 and Unlicense otherwise appear in the source CSV only as `OR` alternatives and are never elected there — every Rust crate offering either one also offers MIT, which we elect instead.

## Third-party components

| Component | Origin | License (SPDX) | Used under |
| --- | --- | --- | --- |
| unicode-normalization | https://github.com/unicode-rs/unicode-normalization | MIT OR Apache-2.0 | MIT |
| tinyvec | https://github.com/Lokathor/tinyvec | Zlib OR Apache-2.0 OR MIT | MIT |
| tinyvec_macros | https://github.com/Soveu/tinyvec_macros | MIT OR Apache-2.0 OR Zlib | MIT |
| yaml-rust2 | https://github.com/Ethiraric/yaml-rust2 | MIT OR Apache-2.0 | MIT |
| arraydeque | https://github.com/andylokandy/arraydeque | MIT OR Apache-2.0 | MIT |
| cfg-if | https://github.com/rust-lang/cfg-if | MIT OR Apache-2.0 | MIT |
| encoding_rs | https://github.com/hsivonen/encoding_rs | (Apache-2.0 OR MIT) AND BSD-3-Clause | MIT + BSD-3-Clause |
| hashbrown | https://github.com/rust-lang/hashbrown | MIT OR Apache-2.0 | MIT |
| hashlink | https://github.com/djc/hashlink | MIT OR Apache-2.0 | MIT |
| foldhash | https://github.com/orlp/foldhash | Zlib | Zlib |
| serde | https://github.com/serde-rs/serde | MIT OR Apache-2.0 | MIT |
| serde_core | https://github.com/serde-rs/serde | MIT OR Apache-2.0 | MIT |
| serde_derive | https://github.com/serde-rs/serde | MIT OR Apache-2.0 | MIT |
| proc-macro2 | https://github.com/dtolnay/proc-macro2 | MIT OR Apache-2.0 | MIT |
| quote | https://github.com/dtolnay/quote | MIT OR Apache-2.0 | MIT |
| syn | https://github.com/dtolnay/syn | MIT OR Apache-2.0 | MIT |
| unicode-ident | https://github.com/dtolnay/unicode-ident | (MIT OR Apache-2.0) AND Unicode-3.0 | MIT + Unicode-3.0 |
| serde_json | https://github.com/serde-rs/json | MIT OR Apache-2.0 | MIT |
| itoa | https://github.com/dtolnay/itoa | MIT OR Apache-2.0 | MIT |
| memchr | https://github.com/BurntSushi/memchr | Unlicense OR MIT | MIT |
| zmij | https://github.com/dtolnay/zmij | MIT | MIT |
| regex | https://github.com/rust-lang/regex | MIT OR Apache-2.0 | MIT |
| regex-automata | https://github.com/rust-lang/regex | MIT OR Apache-2.0 | MIT |
| regex-syntax | https://github.com/rust-lang/regex | MIT OR Apache-2.0 | MIT |
| aho-corasick | https://github.com/BurntSushi/aho-corasick | Unlicense OR MIT | MIT |
| globset | https://github.com/BurntSushi/ripgrep/tree/master/crates/globset | Unlicense OR MIT | MIT |
| bstr | https://github.com/BurntSushi/bstr | MIT OR Apache-2.0 | MIT |
| log | https://github.com/rust-lang/log | MIT OR Apache-2.0 | MIT |
| winnow | https://github.com/winnow-rs/winnow | MIT | MIT |
| time | https://github.com/time-rs/time | MIT OR Apache-2.0 | MIT |
| time-core | https://github.com/time-rs/time | MIT OR Apache-2.0 | MIT |
| time-macros | https://github.com/time-rs/time | MIT OR Apache-2.0 | MIT |
| deranged | https://github.com/jhpratt/deranged | MIT OR Apache-2.0 | MIT |
| num-conv | https://github.com/jhpratt/num-conv | MIT OR Apache-2.0 | MIT |
| powerfmt | https://github.com/jhpratt/powerfmt | MIT OR Apache-2.0 | MIT |
| doublestar | https://github.com/bmatcuk/doublestar | MIT | MIT |
| zerolog | https://github.com/rs/zerolog | MIT | MIT |
| jcs | https://github.com/gowebpki/jcs | Apache-2.0 | Apache-2.0 |
| go-isatty | https://github.com/mattn/go-isatty | MIT | MIT |
| go-colorable | https://github.com/mattn/go-colorable | MIT | MIT |
| x/sys | https://cs.opensource.google/go/x/sys | BSD-3-Clause | BSD-3-Clause |

## License texts

### MIT

Applies to the components below, used under MIT. Copyright holder per component, from `LICENSE-3rdparty.csv`:

- unicode-normalization — kwantam <kwantam@gmail.com> and Manish Goregaokar <manishsmail@gmail.com>
- tinyvec — Lokathor <zefria@gmail.com>
- tinyvec_macros — Soveu <marx.tomasz@gmail.com>
- yaml-rust2 — Chen Yuheng and Ethiraric and David Aguilar <davvid@gmail.com>
- arraydeque — Andy Lok <andylokandy@hotmail.com>
- cfg-if — Alex Crichton <alex@alexcrichton.com>
- encoding_rs — Mozilla Foundation (MIT-elected portion; BSD-3-Clause obligation covered separately below)
- hashbrown — Amanieu d'Antras <amanieu@gmail.com>
- hashlink — The Rust Project Developers (linked-hash-map derivation) and hashlink contributors
- serde — Erick Tryzelaar <erick.tryzelaar@gmail.com> and David Tolnay <dtolnay@gmail.com>
- serde_core — Erick Tryzelaar <erick.tryzelaar@gmail.com> and David Tolnay <dtolnay@gmail.com>
- serde_derive — Erick Tryzelaar <erick.tryzelaar@gmail.com> and David Tolnay <dtolnay@gmail.com>
- proc-macro2 — David Tolnay <dtolnay@gmail.com> and Alex Crichton <alex@alexcrichton.com>
- quote — David Tolnay <dtolnay@gmail.com>
- syn — David Tolnay <dtolnay@gmail.com>
- unicode-ident — David Tolnay <dtolnay@gmail.com> (MIT-elected portion; Unicode-3.0 obligation covered separately below)
- serde_json — Erick Tryzelaar <erick.tryzelaar@gmail.com> and David Tolnay <dtolnay@gmail.com>
- itoa — David Tolnay <dtolnay@gmail.com>
- memchr — Andrew Gallant <jamslam@gmail.com> and bluss
- zmij — David Tolnay <dtolnay@gmail.com>
- regex — The Rust Project Developers and Andrew Gallant <jamslam@gmail.com>
- regex-automata — The Rust Project Developers and Andrew Gallant <jamslam@gmail.com>
- regex-syntax — The Rust Project Developers and Andrew Gallant <jamslam@gmail.com>
- aho-corasick — Andrew Gallant <jamslam@gmail.com>
- globset — Andrew Gallant <jamslam@gmail.com>
- bstr — Andrew Gallant <jamslam@gmail.com>
- log — The Rust Project Developers
- winnow — Ed Page and winnow-rs contributors
- time — Jacob Pratt <open-source@jhpratt.dev> and Time contributors
- time-core — Jacob Pratt <open-source@jhpratt.dev> and Time contributors
- time-macros — Jacob Pratt <open-source@jhpratt.dev> and Time contributors
- deranged — Jacob Pratt <jacob@jhpratt.dev>
- num-conv — Jacob Pratt <jacob@jhpratt.dev>
- powerfmt — Jacob Pratt <jacob@jhpratt.dev>
- doublestar — Bob Matcuk
- zerolog — Olivier Poitrey
- go-isatty — Yasuhiro MATSUMOTO
- go-colorable — Yasuhiro Matsumoto

License text:

```
Permission is hereby granted, free of charge, to any
person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the
Software without restriction, including without
limitation the rights to use, copy, modify, merge,
publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software
is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice
shall be included in all copies or substantial portions
of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF
ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED
TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A
PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT
SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR
IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
DEALINGS IN THE SOFTWARE.
```

### BSD-3-Clause

Mandatory `AND` obligation on `encoding_rs`, covering the WHATWG-supplied data tables the crate generates code from (the crate's own non-generated code is under MIT, listed above; overall crate copyright per the CSV is Mozilla Foundation).

License text, with its own embedded copyright notice as it appears in the crate's `LICENSE-WHATWG` file:

```
Copyright © WHATWG (Apple, Google, Mozilla, Microsoft).

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### Zlib

Applies to `foldhash`. Copyright per the CSV and the license text itself: Orson Peters <orsonpeters@gmail.com>.

License text:

```
Copyright (c) 2024 Orson Peters

This software is provided 'as-is', without any express or implied warranty. In
no event will the authors be held liable for any damages arising from the use of
this software.

Permission is granted to anyone to use this software for any purpose, including
commercial applications, and to alter it and redistribute it freely, subject to
the following restrictions:

1. The origin of this software must not be misrepresented; you must not claim
    that you wrote the original software. If you use this software in a product,
    an acknowledgment in the product documentation would be appreciated but is
    not required.

2. Altered source versions must be plainly marked as such, and must not be
    misrepresented as being the original software.

3. This notice may not be removed or altered from any source distribution.
```

### BSD-3-Clause (golang.org/x/sys)

Applies to `x/sys`, a transitive dependency of `zerolog` (via `go-isatty`). Distinct copyright and text from the `encoding_rs`/WHATWG BSD-3-Clause above.

License text:

```
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

### Apache-2.0

Applies to `jcs`, the RFC 8785 canonicalizer `go/logkit` uses to produce byte-identical wire output across languages.

License text:

```
                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS
```

### Unicode-3.0

Mandatory `AND` obligation on `unicode-ident`, covering the Unicode character-property data tables the crate embeds (the crate's own code is under MIT, listed above; overall crate copyright per the CSV is David Tolnay).

License text, with its own embedded copyright notice as it appears in the crate's `LICENSE-UNICODE` file:

```
UNICODE LICENSE V3

COPYRIGHT AND PERMISSION NOTICE

Copyright © 1991-2023 Unicode, Inc.

NOTICE TO USER: Carefully read the following legal agreement. BY
DOWNLOADING, INSTALLING, COPYING OR OTHERWISE USING DATA FILES, AND/OR
SOFTWARE, YOU UNEQUIVOCALLY ACCEPT, AND AGREE TO BE BOUND BY, ALL OF THE
TERMS AND CONDITIONS OF THIS AGREEMENT. IF YOU DO NOT AGREE, DO NOT
DOWNLOAD, INSTALL, COPY, DISTRIBUTE OR USE THE DATA FILES OR SOFTWARE.

Permission is hereby granted, free of charge, to any person obtaining a
copy of data files and any associated documentation (the "Data Files") or
software and any associated documentation (the "Software") to deal in the
Data Files or Software without restriction, including without limitation
the rights to use, copy, modify, merge, publish, distribute, and/or sell
copies of the Data Files or Software, and to permit persons to whom the
Data Files or Software are furnished to do so, provided that either (a)
this copyright and permission notice appear with all copies of the Data
Files or Software, or (b) this copyright and permission notice appear in
associated Documentation.

THE DATA FILES AND SOFTWARE ARE PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT OF
THIRD PARTY RIGHTS.

IN NO EVENT SHALL THE COPYRIGHT HOLDER OR HOLDERS INCLUDED IN THIS NOTICE
BE LIABLE FOR ANY CLAIM, OR ANY SPECIAL INDIRECT OR CONSEQUENTIAL DAMAGES,
OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION,
ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THE DATA
FILES OR SOFTWARE.

Except as contained in this notice, the name of a copyright holder shall
not be used in advertising or otherwise to promote the sale, use or other
dealings in these Data Files or Software without prior written
authorization of the copyright holder.
```
