# nvrclip

`nvrclip` exports exact MP4 clips from NVR recordings without SmartPSS, DSS Express, iVMS, browser plugins, or vendor background services.

v0 is CLI-first and currently targets Dahua NVRs through raw HTTP CGI endpoints.

## Install

Build a local binary:

```powershell
go run ./tools/build --version 0.1.0
```

The build command updates the Windows program details metadata, regenerates `cmd/nvrclip/resource.syso`, and writes `nvrclip.exe`.

`ffmpeg` must be available on `PATH`, or set `NVRCLIP_FFMPEG` to its full path.

## Config

Create `nvrclip.toml`:

```toml
[office]
type = "dahua"
base_url = "172.20.32.8"
username = "admin"
password_env = "NVRCLIP_OFFICE_PASSWORD"
frame_rate = 25
timeout = "5m"

[office.channels]
1 = "front-door"
2 = "shop cashier"

[warehouse]
type = "hikvision"
base_url = "10.10.37.5"
username = "admin"
password_env = "NVRCLIP_WAREHOUSE_PASSWORD"
insecure_tls = true
frame_rate = 25
timeout = "30m"

[warehouse.channels]
1 = "loading bay"
```

`base_url` can be just an IP/host. nvrclip tries HTTPS first, then HTTP. Set `insecure_tls = true` for NVRs that use self-signed HTTPS certificates.

Passwords can be stored directly in TOML with `password`, or pulled from the environment with `password_env`:

```powershell
$env:NVRCLIP_OFFICE_PASSWORD = "your-password"
```

## Usage

Named alias:

```powershell
.\nvrclip.exe grab shop cashier --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

Explicit NVR and named channel:

```powershell
.\nvrclip.exe download office --channel front-door --around "2026-05-05 14:05" --minutes 10
```

Explicit NVR and numeric channel:

```powershell
.\nvrclip.exe download office --channel 1 --around "2026-05-05 14:05" --minutes 10
```

The Dahua adapter searches for every recording segment overlapping the requested range, downloads each overlapping interval, joins them in order, and writes one MP4.

The Hikvision adapter searches ISAPI recording segments, downloads matching segment files over HTTP(S), trims each part locally, then joins the final MP4.

By default `--mode copy` remuxes without re-encoding. Use `--mode exact` to re-encode with H.264 when you need frame-accurate output.
