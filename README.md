# nvrclip

`nvrclip` exports exact MP4 clips from Dahua and Hikvision NVR recordings without SmartPSS, DSS Express, iVMS, browser plugins, vendor SDKs, or background services.

The basic idea:

```powershell
.\nvrclip.exe grab shop cashier --from "2026-05-06 14:50" --to "2026-05-06 15:20"
```

Output:

```text
shop_cashier_2026-05-06_1450-1520.mp4
```

## Requirements

- Windows, macOS, or Linux
- Go, if building from source
- `ffmpeg` available on `PATH`

On Windows, install FFmpeg with winget:

```powershell
winget install --id Gyan.FFmpeg -e
```

Then reopen PowerShell and verify:

```powershell
ffmpeg -version
```

If that package is unavailable, try:

```powershell
winget install --id BtbN.FFmpeg.GPL -e
```

You can also set `NVRCLIP_FFMPEG` to the full path of `ffmpeg.exe`.

## Build

Build a local binary:

```powershell
go run ./tools/build --version 0.1.0
```

The build command updates Windows Program Details metadata, regenerates `cmd/nvrclip/resource.syso`, and writes `nvrclip.exe`.

## Config

Create `nvrclip.toml` in the working directory.

`base_url` can be just an IP address or hostname. nvrclip tries HTTPS first, then HTTP. If your NVR uses a self-signed HTTPS certificate, set `insecure_tls = true`.

Channels are 1-based, matching the NVR UI.

```toml
[shop]
type = "dahua"
base_url = "172.20.32.8"
username = "admin"
password_env = "NVRCLIP_SHOP_PASSWORD"
frame_rate = 25
timeout = "5m"

[shop.channels]
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
2 = "office"
```

Passwords can be stored directly:

```toml
password = "your-password"
```

Or pulled from environment variables:

```toml
password_env = "NVRCLIP_SHOP_PASSWORD"
```

Set env vars in PowerShell:

```powershell
$env:NVRCLIP_SHOP_PASSWORD = "your-password"
```

## Usage

Use a globally unique channel alias:

```powershell
.\nvrclip.exe grab shop cashier --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

Use an explicit NVR and channel alias:

```powershell
.\nvrclip.exe download shop --channel front-door --around "2026-05-05 14:05" --minutes 10
```

Use an explicit NVR and channel number:

```powershell
.\nvrclip.exe download warehouse --channel 1 --around "2026-05-05 14:05" --minutes 10
```

Use exact start and end times:

```powershell
.\nvrclip.exe download warehouse --channel office --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

Keep downloaded/intermediate files for debugging:

```powershell
.\nvrclip.exe download shop --channel 1 --around "2026-05-05 14:05" --minutes 10 --keep-temp
```

Download only, without producing a final MP4:

```powershell
.\nvrclip.exe download shop --channel 1 --around "2026-05-05 14:05" --minutes 10 --download-only --keep-temp
```

## How It Works

Dahua:

- Searches recording segments through raw CGI.
- Downloads bounded overlapping time ranges where supported.
- Trims/remuxes with FFmpeg.

Hikvision:

- Searches recording segments through ISAPI `ContentMgmt/search`.
- Downloads matching segment files over HTTP(S).
- Trims each downloaded segment locally by timestamp offset.
- Concatenates the trimmed parts into one MP4.

If one requested clip crosses multiple recording files, nvrclip downloads each matching part and shows progress like:

```text
part 1/2 downloading 2026-05-05 10:59:54 - 2026-05-05 11:00:00
part 1/2 done (3.0 MB)
part 2/2 downloading 2026-05-05 11:00:00 - 2026-05-05 11:00:06
part 2/2 done (4.5 MB)
```

## Modes

Default mode:

```powershell
--mode copy
```

This remuxes without re-encoding where possible.

Exact mode:

```powershell
--mode exact
```

This re-encodes with H.264 for more precise cuts when stream-copy cutting is not enough.
