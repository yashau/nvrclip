# nvrclip

`nvrclip` exports exact MP4 clips from Dahua and Hikvision NVR recordings without SmartPSS, DSS Express, iVMS, browser plugins, vendor SDKs, or background services.

The basic idea:

```powershell
.\nvrclip.exe grab "shop cashier" --from "2026-05-06 14:50" --to "2026-05-06 15:20"
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
go run ./tools/build --version 0.2.2
```

The build command updates Windows Program Details metadata, regenerates `cmd/nvrclip/resource.syso`, and writes `nvrclip.exe`.

## Config

Create `nvrclip.toml` in the working directory.

`base_url` can be just an IP address or hostname. nvrclip tries HTTPS first, then HTTP. If your NVR uses a self-signed HTTPS certificate, set `insecure_tls = true`. This option only disables TLS certificate verification; it does not disable HTTPS. To force plain HTTP, include it explicitly, for example `base_url = "http://172.20.32.8"`.

Channels are 1-based, matching the NVR UI.

```toml
[shop]
type = "dahua"
base_url = "172.20.32.8"
username = "admin"
password_env = "NVRCLIP_SHOP_PASSWORD"
auto_time_offset = true
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
auto_time_offset = true
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

### Automatic NVR clock offset

If the PC clock is accurate but the NVR clock may drift, enable automatic time
offset correction in the NVR config:

```toml
[shop]
type = "dahua"
base_url = "172.20.32.8"
username = "admin"
password_env = "NVRCLIP_SHOP_PASSWORD"
auto_time_offset = true
```

You can also enable it for one command:

```powershell
.\nvrclip.exe download shop --channel 1 --from "2026-05-05 14:00" --to "2026-05-05 14:10" --auto-time-offset
```

At the start of the command, nvrclip reads the NVR wall clock and compares it
with the midpoint of the request to reduce network-latency error. It calculates
the offset as `NVR time - PC time` and applies that offset only when talking to
the NVR.

For example, if the NVR is 1 hour slow, a user request for `14:00` is sent to
the NVR as `13:00`. Segment timestamps are translated back before trimming, and
the output filename still uses the requested `14:00` time.

This feature does not change the NVR clock. It assumes the PC and NVR are meant
to use the same local wall-clock time. If nvrclip cannot read the NVR time, the
command fails instead of silently downloading an unadjusted clip.

## Usage

### Command forms

`grab` is the shorthand form. Use it when the channel alias is globally unique across all configured NVRs:

```powershell
.\nvrclip.exe grab "shop cashier" --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

It also accepts a timestamp-centered range:

```powershell
.\nvrclip.exe grab "shop cashier" --around "2026-05-05 14:05" --minutes 10
```

This is equivalent to resolving `"shop cashier"` to its configured NVR and channel, then running the same clip export pipeline as `download`.

For convenience, `grab` joins every argument before the first flag into the channel alias. These two commands are equivalent:

```powershell
.\nvrclip.exe grab "shop cashier" --from "2026-05-05 14:00" --to "2026-05-05 14:10"
.\nvrclip.exe grab shop cashier --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

`download` is the explicit form. Use it when you want to name the NVR directly, when the same channel alias exists on more than one NVR, or when you want to use a numeric channel:

```powershell
.\nvrclip.exe download shop --channel front-door --around "2026-05-05 14:05" --minutes 10
```

Both commands produce a final MP4 by default. Despite the command name, `download` does not only save raw NVR files unless you pass `--download-only`.

### Time shorthands

Use exact start and end times with either command:

```powershell
.\nvrclip.exe grab "shop cashier" --from "2026-05-05 14:00" --to "2026-05-05 14:10"
.\nvrclip.exe download warehouse --channel office --from "2026-05-05 14:00" --to "2026-05-05 14:10"
```

Use `--around` with `--minutes` on either command when you want a clip centered on a timestamp:

```powershell
.\nvrclip.exe grab "shop cashier" --around "2026-05-05 14:05" --minutes 10
.\nvrclip.exe download shop --channel front-door --around "2026-05-05 14:05" --minutes 10
```

That example exports 10 minutes total: 5 minutes before and 5 minutes after `2026-05-05 14:05`.

### Channel selection

Use a channel alias with `download`:

```powershell
.\nvrclip.exe download shop --channel front-door --around "2026-05-05 14:05" --minutes 10
```

Use a channel number with `download`:

```powershell
.\nvrclip.exe download warehouse --channel 1 --around "2026-05-05 14:05" --minutes 10
```

Use exact H.264 re-encoding for precise cuts and safer joins across mixed NVR parts:

```powershell
.\nvrclip.exe download shop --channel 1 --from "2026-05-03 22:50" --to "2026-05-03 23:15" --format exact
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
- Prefers the indexed recording file so the stored codec and resolution are preserved.
- Detects and removes stale indexed-file preambles at timestamp discontinuities, starting from the first valid keyframe.
- Falls back to a bounded CGI export on firmware that does not expose indexed files.
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
--format copy
```

This remuxes without re-encoding where possible.

Exact mode:

```powershell
--format exact
```

This re-encodes with H.264 for more precise cuts when stream-copy cutting is not enough. When a clip spans multiple recording files, exact mode normalizes the parts to matching video dimensions before joining them.

`--mode copy` and `--mode exact` are also accepted.
