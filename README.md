# Transcoding on Demand

[Latest release](https://github.com/livepeer/cli-transcoder/releases/latest)

Our Transcoding on Demand tool is a Command-Line Interface (CLI) that
allows an application to leverage the Livepeer network for video
transcoding, thus removing a complex transcoding scalability
requirement that usually comes with building a video streaming
service. At the same time, it's flexible enough to plug into any
bespoke video workflow, because any backend software can easily invoke
a cli command. It is open source, and currently in beta.

We welcome your feedback at <hello@livepeer.com> and suggestions about
how this tool addresses your needs, or how it can be improved to help
you better address your needs.

You’re also welcome to communicate with our team in the [Livepeer
Discord server][discord] #video-dev channel.

## Installing `lp-transcoder`

What you'll need:

- [A Livepeer.com API Key][1]
- A way to unzip packaged files
- MP4 or TS video that you'd like transcoded into other renditions

### Steps to install:

1. [Download the binary][2] for your OS (Windows, Linux, Mac) and arch.
2. Execute the file
  - If you're on a Mac and get an “unidentified developer” security
     warning, follow [this guide][3] to circumvent it while we work on
     removing this warning.

## Using `lp-transcoder`

Tool accepts `.mp4` and `.ts` file. Output be `.mp4`, `.ts` or `.m3u8`
(HLS manifest).

For HLS output tool will write master playlist and one media playlist
for each transcoding profile.

### Examples

MP4 Output:

    ./lp-transcoder --api-key {API key} transcode name_of_input_video.mp4 name_of_output_video.mp4 -r 256x144 -b 400 --framerate 47 --profile baseline --gop 20s

or

    ./lp-transcoder transcode --api-key API_KEY  input_file_name.mp4 output_file_name.mp4 --profiles config.json

HLS output:

    ./lp-transcoder transcode --api-key API_KEY  input_file_name.mp4 output_dir/output_file_name.m3u8 --profiles config.json output_dir

### Subcommands

The subcommands are structured like this: `lp-transcoder [subcommand]`

- `help` — Global help about the `lp-transcoder`
- `list-presets` — Lists available transcoding presets
- `transcode` — Transcodes video file using Livepeer API

You can also use `lp-transcoder [subcommand] --help` for more information about a specific subcommand.

### Global Flags

The global flags should be specified before the subcommand and are the same for all:

- `-h` / `--help` — display help for lp-transcoder
- `-v` / `--version` — display version of lp-transcoder
- `-a` / `--api-host` — API-host string Livepeer API host (default "[livepeer.com](http://livepeer.com/)")
- `-k` / `--api-key` — API-key string for Livepeer API key

### The `transcode` subcommand

The `transcode` subcommand is used like this:

    lp-transcoder transcode input.[ts|mp4] output.[ts|mp4] [flags]

The first argument after `transcode` is the path to the input file to
be transcoded, and the second one is the path for the output file
where the transcoded renditions will be written. After that one must
specify flags to configure the transcoding job:

- `-h` / `--help` — display specific help for the `transcoder` subcommand
- `-b` / `--bitrate` — set bitrate of the output in `Kbps`
- `-r` / `--resolution` — set resolution of the output

NOTE: Resolution will automatically adjust to be proportional to the
resolution of the input video to avoid stretching of the frames.

- `-f` / `--framerate` — set framerate of the output in frames per second (`fps`)
- `-g` / `--gop` — set GOP size of the output, specified as the time between two keyframes, in seconds.
- `-p` / `--presets` — comma-separated list of transcoding presets (e.g. `P720p30fps16x9`). Use `list-presets` to get a list of presets available to use.
- `-o` / `--profile` — determines hardware acceleration for encoding. Options are `baseline`, `main`, or `high`.
- `--profiles` - file name with desired encoding profiles in JSON format. Example [config.json](config.json)

## Profile structure

```jsonc
{
    "name",
    "width", // number
    "height", // number
    "bitrate", // number, in bits per second
    "fps", // number
    "fpsDen" // number, fps denominator, do not set if fractional fps is not needed
    "gop" // string, for example: 2s
    "profile" // one of - H264Baseline - H264Main - H264High - H264ConstrainedHigh
}
```


  [1]: https://livepeer.com/docs/guides/start-live-streaming/api-key
  [2]: https://github.com/livepeer/cli-transcoder/releases/tag/latest
  [3]: https://support.apple.com/en-gb/guide/mac-help/mh40616/mac
  [discord]: https://discord.gg/uaPhtyrWsF
