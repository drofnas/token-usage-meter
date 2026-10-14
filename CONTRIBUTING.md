# Development

Use Go 1.27.2+, Python 3.11+, and a C++17 compiler with AddressSanitizer/UndefinedBehaviorSanitizer (Clang or GCC). Backend/runtime code has no third-party Go dependencies.

```sh
python3 scripts/check.py
```

This runs Go race tests, vet and executable builds; host lifecycle/configuration tests; 1280 native render states with bounds and paged-frame checks; settings persistence/touch checks; parser mutations; battery checks; the real Go API with the firmware HTTP client; and 15 HTTP framing/deadline cases. All credentials and readings are synthetic. Local test servers bind loopback only. Results and native screen captures are in ignored `artifacts/checks/`.

Compile the full ESP32 firmware without local credentials:

```sh
python3 scripts/firmware.py check-build
```

This requires `requirements.txt` or an installed Device Builder runtime, plus the compiler toolchain downloaded by ESPHome. It writes a synthetic configuration under ignored `artifacts/firmware-check/`. This binary will not connect to your Wi-Fi; use the normal `build` command with private `secrets.yaml` before flashing.

Before publishing:

```sh
go install github.com/zricethezav/gitleaks/v8@v8.30.1
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
govulncheck ./...
python3 -m unittest discover -s tests/security
python3 scripts/check_secrets.py
```

Gitleaks must be on PATH. The publication check scans publishable files and Git history, excluding ignored runtime files, and rejects tracked private paths. Never commit real OAuth credentials, local `.env`/`secrets.yaml`, logs, firmware binaries, or installation state. The Docker context includes only the API source and its shared contract.

The renderer and hit testing share layout coordinates. Keep the 240×320 portrait layout as the default and check both 320×240 rotations after editing it. Review generated PPM captures from the actual renderer; PNGs in `docs/images/` are convenient copies, not separate UI implementations. RGB332 output approximates the panel palette; physical brightness and touch feel still require the CYD.

Fonts are embedded as compact, 2-bit DejaVu glyphs with their license in `assets/fonts/LICENSE`. To regenerate, install Pillow and run `scripts/generate_fonts.py` with a directory containing `DejaVuSans.ttf` and `DejaVuSans-Bold.ttf`; see its `--help`. Normal firmware builds use the checked-in glyph header and need no font download.

Repository map: `internal/collector` reads native credentials and normalizes quotas; `internal/usage` validates v3 snapshots; `internal/api` serves them and battery diagnostics; `firmware/components/meter_network` owns device networking, rendering, touch and saved settings; `scripts/meter.py` owns macOS lifecycle. The two providers fail independently. Never convert an unknown, stale or expired limit into a fresh 100% reading.
