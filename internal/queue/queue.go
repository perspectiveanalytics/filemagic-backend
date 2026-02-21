package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type JobStatus string

const (
	StatusQueued     JobStatus = "queued"
	StatusProcessing JobStatus = "processing"
	StatusDone       JobStatus = "done"
	StatusError      JobStatus = "error"
)

type ConversionType string

const (
	ConversionImageFormat   ConversionType = "image_format"
	ConversionPDFCompress   ConversionType = "pdf_compress"
	ConversionImageCompress ConversionType = "image_compress"
	ConversionOCR            ConversionType = "ocr"
	ConversionMetadataRemove ConversionType = "metadata_remove"
	ConversionPDFMerge       ConversionType = "pdf_merge"
	ConversionImageToPDF     ConversionType = "image_to_pdf"
	ConversionQRCode         ConversionType = "qr_code"
	ConversionCertConvert      ConversionType = "cert_convert"
	ConversionArchive          ConversionType = "archive"
	ConversionMetadataInspect  ConversionType = "metadata_inspect"
	ConversionMarkdownPDF      ConversionType = "markdown_pdf"
	ConversionAudioExtract     ConversionType = "audio_extract"
	ConversionAudioConvert     ConversionType = "audio_convert"
	ConversionVideoCompress    ConversionType = "video_compress"
	ConversionMovToMp4         ConversionType = "mov_to_mp4"
	ConversionVideoToGif       ConversionType = "video_to_gif"
	ConversionDecompress       ConversionType = "decompress"
	ConversionPDFPassword      ConversionType = "pdf_password"
	ConversionSvgToPng         ConversionType = "svg_to_png"
	ConversionPDFExtractImages ConversionType = "pdf_extract_images"
	ConversionPDFEdit          ConversionType = "pdf_edit"
	ConversionFavicon          ConversionType = "favicon"
	ConversionPDFRepair        ConversionType = "pdf_repair"
	ConversionEbookConvert     ConversionType = "ebook_convert"
	ConversionFontConvert      ConversionType = "font_convert"
)

type Job struct {
	ID             string
	Status         JobStatus
	ConversionType ConversionType
	InputPath      string
	OutputPath     string
	Options        map[string]any
	OriginalName   string
	CreatedAt      time.Time
	CompletedAt    time.Time
	Error          string
	InputSize      int64
	OutputSize     int64
	Metadata       map[string]any
	downloaded     atomic.Bool
}

// MarkDownloaded atomically marks the job as downloaded.
// Returns true if this call was the first to mark it (caller owns the download).
func (j *Job) MarkDownloaded() bool {
	return j.downloaded.CompareAndSwap(false, true)
}

// MaxOutputSize is the hard upper limit for any conversion output (100 MB).
// Prevents decompression bombs and runaway tool output.
const MaxOutputSize int64 = 100 << 20

type ProcessFunc func(ctx context.Context, job *Job) error

type Queue struct {
	jobs       sync.Map
	jobChan    chan *Job
	maxSize    int
	processFunc ProcessFunc
	mu         sync.Mutex
	queueOrder []string
}

func New(maxSize int, processFunc ProcessFunc) *Queue {
	return &Queue{
		jobChan:     make(chan *Job, maxSize),
		maxSize:     maxSize,
		processFunc: processFunc,
		queueOrder:  make([]string, 0, maxSize),
	}
}

func (q *Queue) Start(ctx context.Context) {
	go q.worker(ctx)
}

func (q *Queue) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-q.jobChan:
			q.removeFromOrder(job.ID)
			job.Status = StatusProcessing
			q.jobs.Store(job.ID, job)

			err := q.processFunc(ctx, job)
			if err != nil {
				job.Status = StatusError
				job.Error = err.Error()
			} else if job.OutputSize > MaxOutputSize {
				job.Status = StatusError
				job.Error = "output file exceeds maximum allowed size"
			} else {
				job.Status = StatusDone
			}
			job.CompletedAt = time.Now()
			q.jobs.Store(job.ID, job)
		}
	}
}

func (q *Queue) Submit(jobID string, convType ConversionType, inputPath, originalName string, options map[string]any, inputSize int64) (*Job, int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.jobChan) >= q.maxSize {
		return nil, 0, ErrQueueFull
	}

	job := &Job{
		ID:             jobID,
		Status:         StatusQueued,
		ConversionType: convType,
		InputPath:      inputPath,
		OriginalName:   originalName,
		Options:        options,
		InputSize:      inputSize,
		CreatedAt:      time.Now(),
	}

	q.jobs.Store(job.ID, job)
	q.queueOrder = append(q.queueOrder, job.ID)
	q.jobChan <- job

	position := len(q.queueOrder)
	return job, position, nil
}

func (q *Queue) GetJob(jobID string) (*Job, bool) {
	val, ok := q.jobs.Load(jobID)
	if !ok {
		return nil, false
	}
	return val.(*Job), true
}

func (q *Queue) GetPosition(jobID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, id := range q.queueOrder {
		if id == jobID {
			return i + 1
		}
	}
	return 0
}

func (q *Queue) Length() int {
	return len(q.jobChan)
}

func (q *Queue) DeleteJob(jobID string) {
	q.jobs.Delete(jobID)
	q.removeFromOrder(jobID)
}

func (q *Queue) removeFromOrder(jobID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, id := range q.queueOrder {
		if id == jobID {
			q.queueOrder = append(q.queueOrder[:i], q.queueOrder[i+1:]...)
			return
		}
	}
}

func (q *Queue) GetAllJobs() []*Job {
	var jobs []*Job
	q.jobs.Range(func(_, value any) bool {
		jobs = append(jobs, value.(*Job))
		return true
	})
	return jobs
}

type QueueFullError struct{}

func (e QueueFullError) Error() string {
	return "queue is full"
}

var ErrQueueFull = QueueFullError{}
