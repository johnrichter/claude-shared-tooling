# Third-Party Licenses

This file lists the third-party code bundled into this repository's distributed signed binary and reproduces the license text each component requires.

Scope:

- Python side: zero third-party runtime dependencies (stdlib only).
- Go side (`go/build-helpers`): zero third-party dependencies (stdlib only) — nothing to add to `LICENSE-3rdparty.csv` today; a future dependency lands there per `SC-DEPVET`.
- All third-party code is the 35 Rust crates below, statically linked into the distributed binary.

The MIT, BSD-3-Clause, Zlib, and Unicode-3.0 licenses relied on below each require their copyright notice and permission/license text to travel with any binary that includes the licensed code. This file reproduces that attribution and license text for the statically-linked components, satisfying those obligations.

## License election

Several crates offer a choice of license via an SPDX `OR` expression (e.g. `MIT OR Apache-2.0`). For each such crate we elect one license to rely on; an SPDX `AND` term is not a choice — it is a mandatory additional obligation on top of whichever `OR` branch we elect.

Election rule applied: **elect MIT wherever MIT is offered** in an `OR` expression.

Working through all 35 crates under this rule, the licenses we actually rely on reduce to exactly four:

| License | How it applies |
| --- | --- |
| MIT | Elected for every crate whose SPDX expression offers it — the large majority. |
| BSD-3-Clause | Mandatory `AND` term on `encoding_rs` = `(Apache-2.0 OR MIT) AND BSD-3-Clause`. We elect MIT for the `OR`; BSD-3-Clause still applies. |
| Zlib | `foldhash` = `Zlib` with no `OR` alternative — no election, Zlib is the only license. |
| Unicode-3.0 | Mandatory `AND` term on `unicode-ident` = `(MIT OR Apache-2.0) AND Unicode-3.0`. We elect MIT for the `OR`; Unicode-3.0 still applies. |

Apache-2.0 and Unlicense appear in the source CSV as `OR` alternatives but are never elected — every crate offering either one also offers MIT, which we elect instead. Their license texts are not bundled here because we do not rely on them.

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
