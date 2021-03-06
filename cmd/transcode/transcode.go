package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/livepeer/joy4/av"
	"github.com/livepeer/joy4/av/avutil"
	"github.com/livepeer/joy4/format"
	"github.com/livepeer/joy4/format/ts"
	"github.com/livepeer/m3u8"
	"github.com/livepeer/stream-tester/apis/livepeer"
	"github.com/livepeer/stream-tester/apis/mist"
	"github.com/livepeer/stream-tester/model"
	"github.com/livepeer/stream-tester/segmenter"
	"github.com/spf13/cobra"
)

const (
	appName = "livepeer-transcode"
	segLen  = 18 * time.Second
)

func init() {
	format.RegisterAll()
	rand.Seed(time.Now().UnixNano())
}

var errResolution = errors.New("InvalidResolution")
var errH264Profile = errors.New("InvalidH264Profile")

var allowedInputExt = []string{".ts", ".mp4", ".flv"}
var allowedOutputExt = []string{".ts", ".mp4", ".flv", ".m3u8"}
var allowedH264Profiles = map[string]string{"baseline": "H264Baseline", "main": "H264Main", "high": "H264High"}

/*
   - H264Baseline
   - H264Main
   - H264High
   - H264ConstrainedHigh
*/

func parseResolution(resolution string) (int, int, error) {
	res := strings.Split(resolution, "x")
	if len(res) < 2 {
		return 0, 0, errResolution
	}
	w, err := strconv.Atoi(res[0])
	if err != nil {
		return 0, 0, err
	}
	h, err := strconv.Atoi(res[1])
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func parseFps(fps string) (int, int, error) {
	if len(fps) == 0 {
		return 0, 0, nil
	}
	fpp := strings.Split(fps, "/")
	var den uint64
	num, err := strconv.ParseUint(fpp[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing fps %w", err)
	}
	if len(fpp) > 1 {
		den, err = strconv.ParseUint(fpp[1], 10, 32)
		if err != nil {
			return 0, 0, fmt.Errorf("error parsing fps %w", err)
		}
	}
	return int(num), int(den), nil
}

func parmsToProfile(resolution, h264profile, frameRate string, bitrate uint64, gop time.Duration) (*livepeer.Profile, error) {
	var res = &livepeer.Profile{
		Name:    "custom",
		Bitrate: int(bitrate) * 1000,
	}
	if bitrate == 0 {
		return nil, fmt.Errorf("should also specify bitrate")
	}
	if gop > 0 {
		res.Gop = strconv.FormatFloat(gop.Seconds(), 'f', 4, 64)
	}
	w, h, err := parseResolution(resolution)
	if err != nil {
		return nil, err
	}
	res.Width = w
	res.Height = h
	num, den, err := parseFps(frameRate)
	if err != nil {
		return nil, err
	}
	res.Fps = num
	res.FpsDen = den
	if len(h264profile) > 0 {
		if hp, ok := allowedH264Profiles[h264profile]; !ok {
			return nil, errH264Profile
		} else {
			res.Profile = hp
		}
	}

	return res, nil
}

func makeDstName(dst string, i, profiles int) string {
	if profiles == 1 {
		return dst
	}
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	return fmt.Sprintf("%s_%d%s", base, i, ext)
}

func makeMediaPlaylistDstName(dst, mediaName string) string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	// dir := filepath.Dir(base)
	// fnBase := filepath.base(base)
	return fmt.Sprintf("%s_%s%s", base, mediaName, ext)
}

func makeMediaPlaylistName(dst, mediaName string) string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	// dir := filepath.Dir(base)
	base = filepath.Base(base)
	return fmt.Sprintf("%s_%s", base, mediaName)
}

func addPathFrom(dst, fn string) string {
	dir := filepath.Dir(dst)
	return filepath.Join(dir, fn)
}

func getBase(dst string) string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	return filepath.Base(base)
}

func transcode(apiKey, apiHost, src, dst string, presets []string, lprofiles []livepeer.Profile) error {
	lapi := livepeer.NewLivepeer2(apiKey, apiHost, nil, 2*time.Minute)
	lapi.Init()
	glog.Infof("Chosen API server: %s", lapi.GetServer())
	streamName := fmt.Sprintf("tod_%s", time.Now().Format("2006-01-02T15:04:05Z07:00"))
	// stream, err := lapi.CreateStreamEx(streamName, true, nil, standardProfiles...)
	// presets := []string{"P144p30fps16x9", "P240p30fps4x3"}
	var profiles []livepeer.Profile
	if len(lprofiles) > 0 {
		profiles = append(profiles, lprofiles...)
	}
	stream, err := lapi.CreateStreamEx(streamName, false, presets, profiles...)
	if err != nil {
		return err
	}

	defer func(sid string) {
		lapi.DeleteStream(sid)
	}(stream.ID)
	glog.Infof("Created stream id=%s name=%s\n", stream.ID, stream.Name)
	gctx, gcancel := context.WithCancel(context.TODO())
	defer gcancel()
	segmentsIn := make(chan *model.HlsSegment)
	if err = segmenter.StartSegmenting(gctx, src, true, 0, 0, segLen, false, segmentsIn); err != nil {
		return err
	}
	var writtenFiles []string
	outExt := filepath.Ext(dst)
	var playList *m3u8.MasterPlaylist
	var mediaLists []*m3u8.MediaPlaylist
	var mediaSegments [][]*m3u8.MediaSegment
	if outExt == ".m3u8" {
		playList = m3u8.NewMasterPlaylist()
	}
	var outFiles []av.MuxCloser
	var dstNames []string
	var bandwidths []int
	if len(presets) == 0 {
		for i, prof := range profiles {
			name := prof.Name
			if name == "" {
				name = fmt.Sprintf("profile_%d", i)
			}
			presets = append(presets, name)
		}
	}
	for i := range presets {
		if playList != nil {
			mediaSegments = append(mediaSegments, nil)
			bandwidths = append(bandwidths, 0)
		} else {
			dstName := makeDstName(dst, i, len(presets))
			dstNames = append(dstNames, dstName)
			dstFile, err := avutil.Create(dstName)
			if err != nil {
				return fmt.Errorf("can't create out file %w", err)
			}
			outFiles = append(outFiles, dstFile)
		}
	}

	var transcoded [][]byte
	for seg := range segmentsIn {
		if seg.Err == io.EOF {
			break
		}
		if seg.Err != nil {
			err = seg.Err
			break
		}
		glog.Infof("Got segment seqNo=%d pts=%s dur=%s data len bytes=%d\n", seg.SeqNo, seg.Pts, seg.Duration, len(seg.Data))
		started := time.Now()
		transcoded, err = lapi.PushSegment(stream.ID, seg.SeqNo, seg.Duration, seg.Data)
		if err != nil {
			glog.Warningf("Segment push err=%v\n", err)
			break
		}
		glog.Infof("Transcoded %d took %s\n", len(transcoded), time.Since(started))

		for i, segData := range transcoded {
			if playList != nil {
				if bandwidths[i] == 0 {
					bw := len(segData) * 8 / int(seg.Duration.Seconds())
					bandwidths[i] = bw - bw%1000
				}
				segFileName := fmt.Sprintf("%s_%s_%d.ts", getBase(dst), presets[i], seg.SeqNo)
				mseg := new(m3u8.MediaSegment)
				mseg.SeqId = uint64(seg.SeqNo)
				mseg.Duration = seg.Duration.Seconds()
				mseg.URI = segFileName
				mediaSegments[i] = append(mediaSegments[i], mseg)
				segFileFullName := addPathFrom(dst, segFileName)
				if err = os.WriteFile(segFileFullName, segData, 0644); err != nil {
					panic(err)
				}
				writtenFiles = append(writtenFiles, segFileFullName)
			} else {
				demuxer := ts.NewDemuxer(bytes.NewReader(segData))
				if seg.SeqNo == 0 {
					streams, err := demuxer.Streams()
					if err != nil {
						return err
					}
					if err = outFiles[i].WriteHeader(streams); err != nil {
						glog.Warningf("Write header err=%v\n", err)
						return err
					}
				}
				if err = avutil.CopyPackets(outFiles[i], demuxer); err != io.EOF {
					glog.Warningf("Copy packets media %d err=%v\n", i, err)
					return err
				}
			}
		}
	}
	if playList != nil {
		for i, pn := range presets {
			pn = makeMediaPlaylistName(dst, pn)
			var resolution string
			if len(profiles) > 0 {
				resolution = fmt.Sprintf("%dx%d", profiles[i].Width, profiles[i].Height)
			}
			playList.Append(pn+".m3u8", nil, m3u8.VariantParams{Name: pn, Resolution: resolution, Bandwidth: uint32(bandwidths[i])})
			mpl, err := m3u8.NewMediaPlaylist(0, uint(len(mediaSegments[i])))
			if err != nil {
				panic(err)
			}
			mpl.MediaType = m3u8.VOD
			mpl.Live = false
			mpl.TargetDuration = segLen.Seconds()
			for _, seg := range mediaSegments[i] {
				mpl.AppendSegment(seg)
			}
			mediaLists = append(mediaLists, mpl)
		}
		if err := os.WriteFile(dst, playList.Encode().Bytes(), 0644); err != nil {
			glog.Fatal(err)
		}
		writtenFiles = append(writtenFiles, dst)
		for i, mpl := range mediaLists {
			mplName := makeMediaPlaylistDstName(dst, presets[i])
			if err := os.WriteFile(mplName, mpl.Encode().Bytes(), 0644); err != nil {
				glog.Fatal(err)
			}
			writtenFiles = append(writtenFiles, mplName)
		}
	} else {
		for _, outFile := range outFiles {
			if err = outFile.Close(); err != nil {
				return err
			}
		}
	}
	gcancel()
	if err != nil {
		glog.Warningf("Error while segmenting err=%v\n", err)
	}
	glog.Info("Written files:\n")
	for _, fn := range writtenFiles {
		glog.Infof("    %s\n", fn)
	}
	for i := range outFiles {
		// dstName := fmt.Sprintf(dstNameTemplate, i)
		glog.Infof("    %s\n", dstNames[i])
	}
	return nil
}

func main() {
	flag.Set("logtostderr", "true")
	// flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	// flag.Parse()
	flag.CommandLine.Parse(nil)
	/*
		vFlag := flag.Lookup("v")
		verbosity := "6"

		flag.CommandLine.Parse(nil)
		vFlag.Value.Set(verbosity)
	*/
	// var echoTimes int
	var apiKey, apiHost string
	var presets string
	var resolution, frameRate, profile, profiles string
	var bitrateK uint64
	var gop time.Duration

	var cmdTranscode = &cobra.Command{
		Use:   "transcode input.[ts|mp4|flv] output.[ts|mp4|flv]",
		Short: "Transcodes video file using Livepeer API",
		Long:  `Transcodes video file using Livepeer API.`,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			// fmt.Println("transcode: " + strings.Join(args, " "))
			inp := args[0]
			inpExt := filepath.Ext(inp)
			if !stringsSliceContains(allowedInputExt, inpExt) {
				glog.Errorf("Unsupported extension %q for file %q\n", inpExt, inp)
				os.Exit(1)
			}
			output := args[1]
			outputExt := filepath.Ext(output)
			if !stringsSliceContains(allowedOutputExt, outputExt) {
				glog.Errorf("Unsupported extension %q for file %q\n", outputExt, output)
				os.Exit(1)
			}
			if apiKey == "" {
				glog.Error("Should provide --api-key flag\n")
				os.Exit(1)
			}
			if stat, err := os.Stat(inp); errors.Is(err, fs.ErrNotExist) {
				glog.Errorf("File %q does not exists\n", inp)
				os.Exit(1)
			} else if err != nil {
				glog.Errorf("For file: %q err %v\n", inp, err)
				os.Exit(1)
			} else if stat.IsDir() {
				glog.Errorf("Not a file: %q\n", inp)
				os.Exit(1)
			}
			glog.Infof("api key=%q transcode from %q to %q", apiKey, inp, output)
			// presets := []string{"P144p30fps16x9", "P240p30fps4x3"}
			// ffmpeg.P720p30fps16x9
			if (profiles != "" || resolution != "") && presets != "" {
				glog.Errorf("Should not specify preset if profiles or resolution specified\n")
				os.Exit(1)
			}
			var presetsAr []string
			if len(presets) > 0 {
				presetsAr = strings.Split(presets, ",")
				glog.Infof("presets %q ar %+v\n", presets, presetsAr)
				if len(presetsAr) > 0 {
					for _, pr := range presetsAr {
						if _, ok := mist.ProfileLookup[pr]; !ok {
							glog.Errorf("Unknown preset name: %q\n", pr)
							os.Exit(1)
						}
					}
				}
			}

			if len(presets) == 0 && len(resolution) == 0 && len(profiles) == 0 {
				glog.Errorf("Should specify preset or resolution or profiles file name\n")
				os.Exit(1)
			}
			var transcodeProfiles []livepeer.Profile
			if profiles != "" {
				if fc, err := os.ReadFile(profiles); err != nil {
					glog.Errorf("Error reading file %s error: %v\n", profiles, err)
					os.Exit(1)
				} else {
					if err := json.Unmarshal(fc, &transcodeProfiles); err != nil {
						glog.Errorf("Error parsing file %s error: %v\n", profiles, err)
						os.Exit(1)
					}
				}
			} else if resolution != "" {
				if transcodeProfile, err := parmsToProfile(resolution, profile, frameRate, bitrateK, gop); err != nil {
					glog.Errorf("Error parsing arguments: %s\n", err)
					os.Exit(1)
				} else {
					transcodeProfiles = append(transcodeProfiles, *transcodeProfile)
				}
			}
			if err := transcode(apiKey, apiHost, inp, output, presetsAr, transcodeProfiles); err != nil {
				glog.Errorf("Error while transcoding: %v\n", err)
				os.Exit(2)
			}
		},
	}
	cmdTranscode.Flags().StringVarP(&presets, "presets", "p", "", "List of transcoding presets, comma separated (P720p30fps16x9, etc)")
	cmdTranscode.Flags().StringVarP(&frameRate, "framerate", "f", "", "Frame rate")
	cmdTranscode.Flags().StringVarP(&resolution, "resolution", "r", "", "Resolution (1280x720)")
	cmdTranscode.Flags().StringVarP(&profile, "profile", "o", "", "Profile (baseline,main,high)")
	cmdTranscode.Flags().Uint64VarP(&bitrateK, "bitrate", "b", 0, "Bitrate (in Kbit)")
	cmdTranscode.Flags().DurationVarP(&gop, "gop", "g", 0, "Gop (time between keyframes)")
	cmdTranscode.Flags().StringVarP(&profiles, "profiles", "", "", "Names of the JSON file with transcoding configuration")

	var cmdListPresets = &cobra.Command{
		Use:   "list-presets ",
		Short: "Lists transcoding presets",
		Long:  `Lists available transcoding presets`,
		Run: func(cmd *cobra.Command, args []string) {
			glog.Info("Available transcoding presets:\n")
			var ps []string
			for k := range mist.ProfileLookup {
				ps = append(ps, k)
			}
			sort.Strings(ps)
			for _, pr := range ps {
				glog.Infof("  %s\n", pr)
			}
		},
	}

	var rootCmd = &cobra.Command{
		Use:               appName,
		Version:           model.Version,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	rootCmd.PersistentFlags().StringVarP(&apiKey, "api-key", "k", "", "Livepeer API key")
	rootCmd.PersistentFlags().StringVarP(&apiHost, "api-host", "a", "livepeer.com", "Livepeer API host")
	rootCmd.MarkFlagRequired("api-key")
	rootCmd.AddCommand(cmdTranscode, cmdListPresets)
	rootCmd.Execute()
}

func stringsSliceContains(ss []string, st string) bool {
	for _, s := range ss {
		if s == st {
			return true
		}
	}
	return false
}
