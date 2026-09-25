package gallery

import (
	"context"
	"errors"
	"fmt"
	"github.com/hecker-01/go-gallery/internal/downloader"
	"github.com/hecker-01/go-gallery/internal/galleryerrs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

func (c *Client) runDownload(ctx context.Context, url string, cfg DownloadConfig, dl Downloader, fileDL *downloader.HTTPDownloader, fileDLCfg downloader.Config, hlsDL *downloader.YTDLPDownloader) (Result, error) {
	start := time.Now()
	var result Result
	var mu, observerMu sync.Mutex
	var wg sync.WaitGroup
	emit := func(e DownloadEvent) {
		if cfg.Observer != nil {
			observerMu.Lock()
			defer observerMu.Unlock()
			cfg.Observer(e)
		}
	}
	failure := func(err error, info *MediaInfo, path string) {
		mu.Lock()
		result.FailedFiles++
		result.Errors = append(result.Errors, err)
		mu.Unlock()
		emit(DownloadEvent{Kind: EventFailed, Info: info, Path: path, Err: err})
	}
	formatter, err := NewFormatter(cfg.FilenameFormat)
	if err != nil {
		result.Duration = time.Since(start)
		return result, err
	}
	extractCtx, stop := context.WithCancel(ctx)
	defer stop()
	msgs, errs := c.Extract(extractCtx, url)
	seen := make(map[string]bool)
	streak := 0
	matchedRepair := false
	sem := make(chan struct{}, c.concurrency)
	for msg := range msgs {
		if result.StoppedEarly || ctx.Err() != nil {
			stop()
			continue
		}
		if skipped, ok := msg.(Skipped); ok {
			mu.Lock()
			result.UnavailableFiles++
			mu.Unlock()
			emit(DownloadEvent{Kind: EventUnavailable, Info: &MediaInfo{TweetID: skipped.TweetID}, Reason: skipped.Reason, Err: skipped.Cause})
			continue
		}
		if up, ok := msg.(UserProfile); ok {
			if cfg.WriteUserProfile && !cfg.Simulate {
				wg.Add(1)
				go func() { defer wg.Done(); c.writeUserProfile(ctx, cfg, up) }()
			}
			continue
		}
		media, ok := msg.(Media)
		if !ok || media.Info == nil {
			continue
		}
		info := media.Info
		if cfg.Repair != nil && (info.TweetID != cfg.Repair.TweetID || info.Num != cfg.Repair.MediaIndex) {
			continue
		}
		if cfg.Range != nil && !cfg.Range.Contains(info.Num) {
			continue
		}
		if cfg.Filter != nil && !cfg.Filter.Accept(info) {
			continue
		}
		key := info.TweetID + ":" + strconv.Itoa(info.Num)
		if seen[key] {
			continue
		}
		seen[key] = true
		matchedRepair = true
		name := formatter.Format(info.Keywords())
		if name == "" || name == "." {
			name = info.TweetID + "_" + strconv.Itoa(info.Num) + "." + info.Extension
		}
		if cfg.FlatDir {
			name = filepath.Base(name)
		}
		destPath := filepath.Join(cfg.OutputDir, name)
		if cfg.Repair != nil {
			destPath = cfg.Repair.Destination
		}
		if err := CheckOutputPath(cfg.OutputDir, destPath); err != nil {
			failure(err, info, destPath)
			continue
		}
		repairDestination := false
		for _, path := range cfg.RepairDestinations {
			a, _ := filepath.Abs(path)
			b, _ := filepath.Abs(destPath)
			if a == b {
				repairDestination = true
				break
			}
		}
		if err := CheckOutputPath(cfg.OutputDir, destPath+".part"); err != nil {
			failure(err, info, destPath)
			continue
		}
		archiveHit := false
		if c.archive != nil && cfg.Repair == nil && !repairDestination {
			archiveHit, err = c.archive.Has(ctx, key)
			if err != nil {
				failure(err, info, destPath)
				continue
			}
		}
		fi, statErr := os.Stat(destPath)
		existing := statErr == nil && fi.Mode().IsRegular() && fi.Size() > 0
		if statErr != nil && !os.IsNotExist(statErr) {
			failure(statErr, info, destPath)
			continue
		}
		if archiveHit || existing {
			mu.Lock()
			result.SkippedFiles++
			mu.Unlock()
			kind := EventExisting
			if archiveHit {
				kind = EventArchive
			}
			emit(DownloadEvent{Kind: kind, Info: info, Path: destPath})
			if existing && !cfg.Simulate {
				for _, pp := range cfg.PostProcessors {
					if bp, ok := pp.(BackfillProcessor); ok {
						if err := bp.OnExisting(ctx, destPath, info); err != nil {
							failure(err, info, destPath)
						}
					}
				}
			}
			streak++
			if cfg.StopAfterExisting > 0 && streak >= cfg.StopAfterExisting {
				result.StoppedEarly = true
				stop()
			}
			continue
		}
		streak = 0
		if cfg.Simulate {
			mu.Lock()
			result.TotalFiles++
			mu.Unlock()
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			stop()
			continue
		}
		wg.Add(1)
		go func(info *MediaInfo, mediaURL, destPath, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				failure(err, info, destPath)
				return
			}
			record := RepairRecord{Destination: destPath, TweetID: info.TweetID, MediaIndex: info.Num, SourceURL: "https://x.com/i/status/" + info.TweetID}
			if err := SaveRepairRecord(cfg.OutputDir, record); err != nil {
				failure(err, info, destPath)
				return
			}
			for _, pp := range cfg.PostProcessors {
				if err := pp.OnPrepare(ctx, info); err != nil {
					failure(err, info, destPath)
					return
				}
			}
			var dlErr error
			switch {
			case dl != nil:
				f, err := os.Create(destPath + ".part")
				if err != nil {
					failure(err, info, destPath)
					return
				}
				dlErr = dl.Download(ctx, mediaURL, f, cfg)
				closeErr := f.Close()
				if dlErr == nil {
					dlErr = closeErr
				}
				if dlErr == nil {
					dlErr = os.Rename(destPath+".part", destPath)
				}
			case isHLSURL(mediaURL):
				dlErr = downloadHLS(ctx, hlsDL, mediaURL, destPath)
			default:
				dlErr = fileDL.DownloadToFile(ctx, mediaURL, destPath, fileDLCfg)
			}
			if dlErr != nil {
				var nfe *galleryerrs.NotFoundError
				var ae *galleryerrs.AuthorizationError
				if errors.As(dlErr, &nfe) || errors.As(dlErr, &ae) {
					mu.Lock()
					result.UnavailableFiles++
					result.Errors = append(result.Errors, dlErr)
					mu.Unlock()
					emit(DownloadEvent{Kind: EventUnavailable, Info: info, Path: destPath, Reason: "unavailable", Err: dlErr})
				} else {
					failure(dlErr, info, destPath)
				}
				_ = runPostProcessors(ctx, cfg.PostProcessors, destPath, info, dlErr)
				return
			}
			if ppErr := runPostProcessors(ctx, cfg.PostProcessors, destPath, info, nil); ppErr != nil {
				failure(ppErr, info, destPath)
			}
			if c.archive != nil {
				if err := c.archive.Put(ctx, key); err != nil {
					failure(err, info, destPath)
				}
			}
			if err := RemoveRepairRecord(cfg.OutputDir, record); err != nil {
				failure(err, info, destPath)
			}
			var size int64
			if fi, err := os.Stat(destPath); err == nil {
				size = fi.Size()
			}
			mu.Lock()
			result.TotalFiles++
			mu.Unlock()
			reason := ""
			if cfg.Repair != nil || repairDestination {
				reason = "repair"
			}
			emit(DownloadEvent{Kind: EventCompleted, Info: info, Path: destPath, Bytes: size, Reason: reason})
			c.logger.Info(destPath)
		}(info, media.URL, destPath, key)
	}
	wg.Wait()
	result.Duration = time.Since(start)
	extractErr := <-errs
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if extractErr != nil && !(result.StoppedEarly && errors.Is(extractErr, context.Canceled)) {
		return result, extractErr
	}
	if cfg.Repair != nil && !matchedRepair && result.UnavailableFiles == 0 {
		return result, fmt.Errorf("repair media %s:%d was not returned by the post", cfg.Repair.TweetID, cfg.Repair.MediaIndex)
	}
	return result, nil
}
