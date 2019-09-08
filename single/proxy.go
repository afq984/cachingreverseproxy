// Package single provides a caching reverse proxy that uses a single upstream host
package single

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"sync"
	"time"
)

func statusError(w http.ResponseWriter, code int) {
	http.Error(w, http.StatusText(code), code)
}

type CachingReverseProxy struct {
	client         *http.Client
	upstreamPrefix string
	cacheDir       string
	objectHandles  sync.Map
}

func NewCachingReverseProxy(upstreamPrefix string, cacheDir string) *CachingReverseProxy {
	return &CachingReverseProxy{
		client:         &http.Client{},
		upstreamPrefix: upstreamPrefix,
		cacheDir:       cacheDir,
	}
}

var _ http.Handler = &CachingReverseProxy{}

func (p *CachingReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodHead && r.Method != http.MethodGet {
		fmt.Fprintln(w, "Only HEAD or GET allowed")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cleanPath := path.Clean("/" + r.URL.Path)
	cachePath := path.Join(p.cacheDir, cleanPath)
	upstreamReq, err := http.NewRequest(
		r.Method,
		p.upstreamPrefix+cleanPath,
		nil,
	)
	if err != nil {
		panic(err)
	}

	var cacheFile *os.File
	cacheFile, err = os.Open(cachePath)
	var cacheModTime time.Time
	if err == nil {
		defer cacheFile.Close()
		var stat os.FileInfo
		stat, err = cacheFile.Stat()
		if err != nil {
			panic(err)
		}
		cacheModTime = stat.ModTime().UTC()
		upstreamReq.Header.Set("If-Modified-Since", cacheModTime.Format(http.TimeFormat))
	} else if !os.IsNotExist(err) {
		log.Printf("open %s: %v", cachePath, err)
	}

	var upstreamResp *http.Response
	upstreamResp, err = p.client.Do(upstreamReq)
	if err != nil {
		statusError(w, http.StatusBadGateway)
		log.Printf("Error performing request %s: %v", upstreamReq.URL, err)
		return
	}
	if upstreamResp.StatusCode == http.StatusNotModified {
		log.Printf("serving locally cached %s", cachePath)
		http.ServeContent(w, r, path.Base(cachePath), cacheModTime, cacheFile)
		return
	}

	upstreamLastModified, modTimeErr := time.Parse(http.TimeFormat, upstreamResp.Header.Get("Last-Modified"))
	if modTimeErr != nil {
		// log.Println("upstream does not provide Last-Modified for", cleanPath)
	}

	hasAcceptRangeBytes := false
	for _, val := range upstreamResp.Header["Accept-Ranges"] {
		if val == "bytes" {
			hasAcceptRangeBytes = true
			break
		}
	}
	if !hasAcceptRangeBytes {
		// log.Println("upstream does not provide Accept-Ranges: bytes for", cleanPath)
	}

	if r.Method == http.MethodGet && upstreamResp.StatusCode == http.StatusOK && hasAcceptRangeBytes && upstreamResp.ContentLength != -1 && modTimeErr == nil {
		log.Println(cleanPath, "is cachable")
		i, _ := p.objectHandles.LoadOrStore(
			cleanPath,
			&objectHandle{proxy: p, cleanPath: cleanPath},
		)
		handle := i.(*objectHandle)
		var rd ReadSeekCloser
		rd, err = handle.Get(upstreamResp.Body, upstreamLastModified, upstreamResp.ContentLength, cachePath)
		if err != nil {
			statusError(w, http.StatusInternalServerError)
			log.Printf("Cannot get %s: %v", cleanPath, err)
			return
		}
		http.ServeContent(w, r, path.Base(cleanPath), upstreamLastModified, rd)
		rd.Close()
		return
	}

	log.Println("not caching", cleanPath)
	if upstreamResp.ContentLength != -1 {
		w.Header().Set("Content-Length", strconv.FormatInt(upstreamResp.ContentLength, 10))
	}
	if modTimeErr == nil {
		w.Header().Set("Last-Modified", upstreamResp.Header.Get("Last-Modified"))
	}
	if contentType, ok := upstreamResp.Header["Content-Type"]; ok {
		w.Header()["Content-Type"] = contentType
	}
	if hasAcceptRangeBytes {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(upstreamResp.StatusCode)
	if r.Method == http.MethodGet {
		_, err = io.Copy(w, upstreamResp.Body)
		if err != nil {
			log.Println("error copying response", err)
		}
		upstreamResp.Body.Close()
	}
}

type objectHandle struct {
	proxy          *CachingReverseProxy
	cleanPath      string
	once           sync.Once
	tempPath       string
	trackingWriter *trackingWriter
}

func (h *objectHandle) Get(body io.ReadCloser, modTime time.Time, size int64, cachePath string) (ReadSeekCloser, error) {
	var err error
	shouldCloseBody := true
	defer func() {
		if shouldCloseBody {
			body.Close()
		}
	}()
	cacheDir := path.Dir(cachePath)

	h.once.Do(func() {
		err = os.MkdirAll(cacheDir, 0755)
		if err != nil {
			log.Printf("Could not create directory %q for cached file: %v", cacheDir, err)
			return
		}
		var tempFile *os.File
		tempFile, err = ioutil.TempFile(cacheDir, path.Base(cachePath)+".part.*")
		if err != nil {
			log.Println("Could not create tempfile:", err)
			return
		}
		h.tempPath = tempFile.Name()
		h.trackingWriter = newTrackingWriter(tempFile, size)
		shouldCloseBody = false
		go func() {
			defer body.Close()
			log.Println("starting download:", h.tempPath)
			var err error
			n, err := io.Copy(h.trackingWriter, body)
			if err != nil {
				log.Println("Unexpected error downloading", h.tempPath)
			} else {
				log.Printf("Finished downloading %s, size: %d", h.tempPath, n)

				err = os.Chtimes(h.tempPath, time.Now(), modTime)
				if err != nil {
					log.Println("Cannot change modtime of", h.tempPath)
				}
			}
			logIfErr := func(msg string, err error) {
				if err != nil {
					log.Println(msg, h.tempPath, err)
				}
			}
			logIfErr("close", h.trackingWriter.Close())

			if err == nil {
				logIfErr("rename", os.Rename(h.tempPath, cachePath))
			} else {
				logIfErr("remove", os.Remove(h.tempPath))
			}

			h.proxy.objectHandles.Delete(h.cleanPath)
		}()
	})

	var rfile *os.File
	rfile, err = os.Open(h.tempPath)
	if err == nil {
		log.Println("tracking", h.tempPath)
		return &partiallyDownloadedFile{
			wrapped:        rfile,
			trackingWriter: h.trackingWriter,
		}, nil
	}
	if os.IsNotExist(err) {
		log.Println("using downloaded", cachePath)
		rfile, err = os.Open(cachePath)
		if err == nil {
			return rfile, nil
		}
		log.Println("unexpected error opening", cachePath)
	} else {
		log.Println("unexpected error opening", h.tempPath)
	}
	return nil, err
}

type trackingWriter struct {
	wrapped       io.WriteCloser
	size          int64
	written       int64
	updateWritten chan int64
	done          chan struct{}
}

func newTrackingWriter(w io.WriteCloser, size int64) *trackingWriter {
	return &trackingWriter{
		wrapped:       w,
		size:          size,
		updateWritten: make(chan int64),
		done:          make(chan struct{}),
	}
}

func (w *trackingWriter) Write(p []byte) (n int, err error) {
	n, err = w.wrapped.Write(p)
	w.update(n)
	return
}

func (w *trackingWriter) update(n int) {
	w.written += int64(n)
	for {
		select {
		case w.updateWritten <- w.written:
		default:
			return
		}
	}
}

func (w *trackingWriter) Close() (err error) {
	err = w.wrapped.Close()
	close(w.done)
	return err
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type partiallyDownloadedFile struct {
	wrapped        ReadSeekCloser
	trackingWriter *trackingWriter
	readyPos       int64
	pos            int64
	seekBeforeRead bool
}

func (r *partiallyDownloadedFile) Read(p []byte) (n int, err error) {
	if r.pos >= r.trackingWriter.size {
		return 0, io.EOF
	}
loop:
	for r.pos >= r.readyPos {
		select {
		case r.readyPos = <-r.trackingWriter.updateWritten:
		case <-r.trackingWriter.done:
			r.readyPos = r.trackingWriter.written
			break loop
		}
	}
	if r.seekBeforeRead {
		r.pos, err = r.wrapped.Seek(r.pos, io.SeekStart)
		if err != nil {
			return 0, err
		}
		r.seekBeforeRead = false
	}
	if r.readyPos-r.pos < int64(len(p)) {
		p = p[:r.readyPos-r.pos]
	}
	n, err = r.wrapped.Read(p)
	r.pos += int64(n)
	return n, err
}

func (r *partiallyDownloadedFile) Seek(offset int64, whence int) (int64, error) {
	var pos int64
	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos = r.pos + offset
	case io.SeekEnd:
		pos = r.trackingWriter.size + offset
	default:
		return r.pos, fmt.Errorf("unsupported seek whence: %d", whence)
	}
	if pos < 0 {
		return r.pos, fmt.Errorf("seek position %d < 0", r.pos)
	}
	r.pos = pos
	r.seekBeforeRead = true
	return pos, nil
}

func (r *partiallyDownloadedFile) Close() error {
	return r.wrapped.Close()
}

var _ ReadSeekCloser = &partiallyDownloadedFile{}
