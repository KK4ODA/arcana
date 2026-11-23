bergamot-translator.js: the Emscripten loader for Mozilla's Bergamot
translation engine (v0.6.0+eea6e5a8), as shipped in Firefox's release
branch (toolkit/components/translations/bergamot-translator/). It is
unmodified. License: MPL-2.0 (LICENSE.txt).

It matches the WebAssembly engine "bergamot-translator" 4.0 (release
v0.6.0) that Firefox downloads from its Remote Settings collection
translations-wasm-v2; Arcana's catalog entry arcana.translation-engine pins
that file by SHA-256. The engine itself is downloaded on request, not
embedded in Arcana.
