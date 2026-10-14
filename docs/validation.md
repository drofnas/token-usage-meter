# Validation — 2026-10-13

Validation target: one macOS host serving one 240×320 CYD, with independent Claude and Codex quota sources. The original Codex meter checkout and installation were left unchanged.

## Automated checks

| Check | Result |
| --- | --- |
| Go backend | Race tests, vet, and both executable builds passed with Go 1.27.2 |
| Vulnerability scan | `govulncheck` v1.9.0 reported no vulnerabilities |
| Desktop lifecycle and configuration | 17 synthetic tests passed |
| Display and settings | 1280 combinations of rotation, toggles, menu state, quota and reset counts passed with ASan/UBSan; full-frame and paged rendering matched |
| Touch geometry | Every control hit-area point and all native coordinates checked in all four orientations |
| Persistence | Immediate sync, restart restore, corrupt saved values, failed saves and disabled child controls checked using a fake store |
| Usage contract | Malformed input, old snapshots, independent provider errors, cache restart, reset-count validation and stale/zero counter hiding checked |
| Device transport | Real Go API to firmware HTTP client/decoder, including reset counts, passed; 15 framing and deadline cases passed |
| Battery diagnostics | Native checks passed; bounded history uses 648 bytes |
| Publication security | Four scanner regression tests passed; Gitleaks and publication-path checks found no secrets in the candidate |
| Docker | API container built with the pinned Go 1.27.2 image and restricted source-only build context |
| Full firmware | Configured ESPHome 2026.8.0 / ESP-IDF 5.5.5 build passed |

Run the commands in [CONTRIBUTING.md](../CONTRIBUTING.md) to reproduce the synthetic checks. CI runs the publication, vulnerability, backend, desktop, native firmware/UI and container checks on pushes and pull requests. Full ESP32 compilation uses the separate firmware helper.

## Renderer and hardware

Native captures in [images](images/) use synthetic readings and were visually inspected at 240×320 and 320×240. The reset counter uses the original reload icon, aligned to the right of each provider title. Maximum counts fit without touching the title; zero, unavailable and stale counts stay hidden. Claude currently supplies no supported reset count.

The configured firmware was flashed to a 4 MB ESP32-D0WD-V3 CYD. All four written regions passed hash verification. The updated host services were installed and started with login startup enabled. Live API responses reported both providers available. A 75-second serial capture recorded two successful device polls, fresh provider data, portrait mode, restored saved settings and no observed crash. The user confirmed the portrait layout and Settings screen before the reset-counter update.

The configured build uses 787,979 bytes of its 1,835,008-byte application partition (42.9%) and 48,076 bytes of static DRAM (26.6%). The display buffer is 19,200 bytes and the frozen UI frame is 193 bytes. During the short hardware check, steady free heap was approximately 159 KB and a complete portrait redraw took about 203 ms. These measurements describe this board and build, not a long-duration benchmark.

Configured binaries, live readings, credentials, installation state, serial logs and rollback artifacts remain in ignored local paths and are excluded from publication.

## Remaining checks

The new counter's physical appearance was checked only through the native renderer. Persistence after actual power removal, touch feel in every rotation, long-duration battery operation and recovery after provider token expiry still require hardware or live-account checks. Saved settings did restore across the measured serial reset.

For fresh data, the Mac must remain awake and Docker must be running. Provider credentials remain managed by their native applications. The device API is intended only for a trusted private LAN.
