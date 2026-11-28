# Third-party notices

µPi's own files are under the [MIT license](LICENSE). µPi is built on the following projects, each under its own license.

| Project | Used for | License |
|---|---|---|
| [PiG](https://github.com/MichaelKinsy/PiG), © Michael Kinsy | The harness µPi is built on: most of this repository's Go code is PiG's. Its own notices, including the third-party software it embeds, are in [`NOTICE`](NOTICE) | MIT |
| [Pi](https://github.com/earendil-works/pi) (coding agent), © 2025 Mario Zechner | The agent PiG implements in Go; Pi's extension SDK and parts of its distribution are bundled for the TypeScript extension runtime | MIT |
| [Pi-Bolt](https://github.com/opensec-git/Pi-Bolt), © 2026 OpenSec | µPi 0.1 was a fork of it. Code ported from it carries its copyright line (for example `tui/tui_alt_screen_scroll.go`) and names it where it is used | MIT |

The MIT license of each project above:

> Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files
> (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify,
> merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions: The above copyright notice and this permission notice shall be included
> in all copies or substantial portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
> IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF
> CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

The release executables of µPi 0.1 (the `pi-bolt` branch and its `upi-v0.1.x` releases) also contained Bun, a modified
JavaScriptCore (LGPL-2.0), ICU and a glibc sysroot; their notices and the corresponding source are on that branch. The Go builds
from 0.2 on contain none of them.
