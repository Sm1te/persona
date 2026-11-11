package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Reading a text aloud (POST /speak, docs/assistant/voice.md "Reading a text aloud", #5004): the iPhone says its
// answers in a meeting outside a call in the realtime voice the owner hears in calls (their model and voice), not in
// the phone's on-device one. One upstream session per text, with no tools and instructions that it only reads texts
// out (voice/read_aloud); the reply's audio is the answer's body as it comes, and the session closes once the reply is
// over. Nothing is written before the first audio, so the phone falls back to its own voice on any error status.

// speakMostRunes is the longest text it reads (a meeting answer is at most 400 characters).
const speakMostRunes = 1000

// How long a read-aloud waits (variables so tests can shorten them): for the first audio once the session started;
// after audio, for more of it when the service does not say its reply is over; and in all.
var (
	speakFirstAudio = 15 * time.Second
	speakQuiet      = 3 * time.Second
	speakMost       = 150 * time.Second
)

// The codes of POST /speak's errors besides the calls' (info.go).
const (
	codeBadRequest = "bad_request" // 400: no text, too long, a malformed model or voice
	codeForbidden  = "forbidden"   // 403: not a full device (another extension, an answer-only device)
	codeNoAudio    = "no_audio"    // 504: the session started and said nothing in time
)

// headerAudioRate is the sample rate of the answer's audio: mono PCM16, little-endian.
const headerAudioRate = "X-Persona-Audio-Rate"

// langTag is a language tag as far as the instructions need to trust one (BCP 47's shape, not its registry).
var langTag = lazyre.New(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8}){0,3}$`)

type speakRequest struct {
	Text  string `json:"text"`
	Model string `json:"model"` // "<provider>/<model>", "" the default
	Voice string `json:"voice"` // "" the model's default
	Lang  string `json:"lang"`  // BCP 47: the language the text is in, as far as the phone knows
}

// speech is one read-aloud, for its log line: ids, sizes and times only, never the text.
type speech struct {
	pl       *Plugin
	id       int64
	began    time.Time
	runes    int
	provider string
	model    string
	voice    string
	bytes    int
	rate     int
}

func (pl *Plugin) serveSpeak(w http.ResponseWriter, r *http.Request) {
	// Others hear what it says, and it costs a session of the owner's service: a full device's (or, on an old
	// socket-only deployment that names no caller, the hub's own).
	if role := r.Header.Get(extwire.HeaderRole); role != "" && role != roleFull {
		speakError(w, http.StatusForbidden, codeForbidden, "only a full device can have a text read aloud")
		return
	}
	var req speakRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		speakError(w, http.StatusBadRequest, codeBadRequest, "the body is JSON: {text, model?, voice?, lang?}")
		return
	}
	text := strings.TrimSpace(req.Text)
	n := runeLen(text)
	switch {
	case n == 0:
		speakError(w, http.StatusBadRequest, codeBadRequest, "no text to read")
		return
	case n > speakMostRunes:
		speakError(w, http.StatusBadRequest, codeBadRequest, fmt.Sprintf("the text is longer than %d characters", speakMostRunes))
		return
	}
	if req.Model != "" {
		if _, _, ok := splitModel(req.Model); !ok {
			speakError(w, http.StatusBadRequest, codeBadRequest, "model is not <provider>/<model>")
			return
		}
	}
	if req.Voice != "" && !voicePattern.MatchString(req.Voice) {
		speakError(w, http.StatusBadRequest, codeBadRequest, "voice is not a voice id")
		return
	}
	textLang := req.Lang
	if !langTag.MatchString(textLang) {
		textLang = "" // only a hint: a word that is not a language tag is left out
	}
	lang := i18n.Resolve(r.Header.Get(extwire.HeaderLang))
	if lang == "" {
		lang = pl.link.language()
	}
	if lang == "" {
		lang = i18n.ZhHans
	}

	pl.active.Add(1) // a redeploy waits for it as for a call (GET /health)
	defer pl.active.Add(-1)
	sp := &speech{pl: pl, id: pl.speaks.Add(1), began: time.Now(), runes: n, voice: req.Voice}
	instructions := prompts.RenderVoiceReadAloud(prompts.VoiceReadAloud{Lang: string(lang), Owner: pl.cfg.Owner, TextLang: textLang})
	sp.serve(w, r, req.Model, text, instructions)
}

// serve opens the session, has it read text and streams its audio to w.
func (sp *speech) serve(w http.ResponseWriter, r *http.Request, modelID, text, instructions string) {
	ctx, cancel := context.WithTimeout(r.Context(), speakMost)
	defer cancel()
	p, model, err := sp.pl.cat.resolve(ctx, modelID)
	if errors.Is(err, errNoProvider) {
		// The phone's model is of an upstream no longer set up: the default one, as a call does; the voice was the
		// other upstream's.
		sp.pl.logf("speak %d: model %q is gone, the default one instead", sp.id, modelID)
		p, model, err = sp.pl.cat.resolve(ctx, "")
		sp.voice = ""
	}
	if err != nil {
		sp.failed(w, err)
		return
	}
	sp.provider, sp.model = p.ID, model
	wr := newWire(p.Dialect)
	up, rate, err := sp.open(ctx, p, wr, model, instructions)
	if err != nil {
		sp.failed(w, err)
		return
	}
	sp.rate = rate
	// The reader's own context: cancelling a WebSocket's read closes it at once, and a reply that is over closes the
	// session politely (end), which needs the reader until the service answers the close.
	rctx, stopReading := context.WithCancel(context.Background())
	polite := false
	defer func() {
		if !polite {
			stopReading()
		}
	}()
	type read struct {
		events []event
		err    error
	}
	reads := make(chan read, 16)
	go func() {
		for {
			typ, b, err := up.Read(rctx)
			if err != nil {
				select {
				case reads <- read{err: err}:
				case <-rctx.Done():
				}
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if evs := wr.decode(b); len(evs) > 0 {
				select {
				case reads <- read{events: evs}:
				case <-rctx.Done():
					return
				}
			}
		}
	}()
	for _, f := range wr.say(text, "") {
		if err := sp.writeUp(ctx, up, f); err != nil {
			_ = up.CloseNow()
			sp.failed(w, &upstreamError{code: codeUpstreamFailed, err: fmt.Errorf("sending the text: %w", err)})
			return
		}
	}

	began := false
	first := time.NewTimer(speakFirstAudio)
	defer first.Stop()
	quiet := time.NewTimer(time.Hour)
	quiet.Stop()
	defer quiet.Stop()
	// end closes the session once the answer is over: politely, after the body is done (the handler returns at once).
	end := func(outcome string) {
		sp.logEnd(outcome)
		polite = true
		go func() {
			defer stopReading()
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = writeJSON(cctx, up, wr.close())
			cancel()
			_ = up.Close(websocket.StatusNormalClosure, "bye")
		}()
	}
	for {
		select {
		case <-ctx.Done():
			_ = up.CloseNow()
			switch {
			case r.Context().Err() != nil:
				sp.logEnd("the app hung up")
			case began:
				sp.logEnd(fmt.Sprintf("cut at %v", speakMost))
			default:
				sp.failedCode(w, http.StatusGatewayTimeout, codeNoAudio, "the voice said nothing in time")
			}
			return
		case <-first.C:
			if !began {
				_ = up.CloseNow()
				sp.failedCode(w, http.StatusGatewayTimeout, codeNoAudio, "the voice said nothing in time")
				return
			}
		case <-quiet.C:
			end("done (quiet)")
			return
		case rd := <-reads:
			if rd.err != nil {
				_ = up.CloseNow()
				if !began {
					sp.failed(w, &upstreamError{code: codeUpstreamFailed, err: fmt.Errorf("the session dropped before it said anything: %w", rd.err)})
					return
				}
				sp.logEnd("the session dropped")
				return
			}
			for _, e := range rd.events {
				switch e.kind {
				case evAudio:
					if !began {
						began = true
						h := w.Header()
						h.Set("Content-Type", "audio/pcm")
						h.Set(headerAudioRate, strconv.Itoa(sp.rate))
						h.Set("Cache-Control", "no-store")
						w.WriteHeader(http.StatusOK)
					}
					if _, err := w.Write(e.audio); err != nil {
						_ = up.CloseNow()
						sp.logEnd("the app hung up")
						return
					}
					_ = http.NewResponseController(w).Flush()
					sp.bytes += len(e.audio)
					quiet.Reset(speakQuiet)
				case evTurnEnd, evInterrupted:
					// Before audio, a reply without any (one that only wrote text): what it says may still come.
					if began {
						end("done")
						return
					}
				case evError:
					if began {
						_ = up.CloseNow()
						sp.logEnd("the service's error " + firstNonEmpty(e.code, "(no code)"))
						return
					}
					_ = up.CloseNow()
					refused := &sessionRefused{code: e.code, param: e.param, text: e.text}
					if e.code == codeUpstreamUnavailable {
						sp.failed(w, &upstreamError{code: codeUpstreamUnavailable, err: refused})
					} else {
						sp.failed(w, &upstreamError{code: codeUpstreamFailed, err: refused})
					}
					return
				}
			}
		}
	}
}

// open dials the upstream and starts the session; a service that refuses the voice (not one that is down) gets the
// same session once more with its default voice.
func (sp *speech) open(ctx context.Context, p *Provider, w wire, model, instructions string) (upLink, int, error) {
	sctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	start := func(voice string) (upLink, int, error) {
		up, err := dialUpstream(sctx, p, w, model)
		if err != nil {
			return nil, 0, err
		}
		rate, err := startUpstream(sctx, up, w, p,
			session{Model: model, Voice: voice, InputRate: p.inputRate(), Instructions: instructions, ReadAloud: true})
		if err != nil {
			return nil, 0, err
		}
		return up, rate, nil
	}
	up, rate, err := start(sp.voice)
	var refused *sessionRefused
	var ue *upstreamError
	if err != nil && sp.voice != "" && errors.As(err, &refused) && !errors.As(err, &ue) {
		sp.pl.logf("speak %d: the service refused the session with voice %s (%s); again with the default voice", sp.id, sp.voice,
			firstNonEmpty(refused.code, "no code"))
		sp.voice = ""
		up, rate, err = start("")
	}
	return up, rate, err
}

// writeUp writes v to up, waiting at most upWriteTimeout.
func (sp *speech) writeUp(ctx context.Context, up upLink, v any) error {
	wctx, cancel := context.WithTimeout(ctx, upWriteTimeout)
	defer cancel()
	return writeJSON(wctx, up, v)
}

// failed answers a failure that came before any audio, with the code the phone knows it by. The answer and the log
// say what went wrong without the service's address (which may carry a key) or anything of the text.
func (sp *speech) failed(w http.ResponseWriter, err error) {
	code, status := errCode(err), 0
	var hs *httpStatusError
	if errors.As(err, &hs) { // the service's model list, read for its default model
		switch hs.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			code = codeUpstreamForbidden
		case http.StatusServiceUnavailable:
			code = codeUpstreamUnavailable
		}
	}
	msg := ""
	switch code {
	case codeNoUpstream:
		status, msg = http.StatusServiceUnavailable, "no realtime voice service is set up yet"
	case codeUpstreamUnavailable:
		status, msg = http.StatusServiceUnavailable, "the realtime voice service is unavailable for the moment"
	case codeUpstreamForbidden:
		status, msg = http.StatusBadGateway, "the realtime voice service refused the key"
	default:
		status, code, msg = http.StatusBadGateway, codeUpstreamFailed, "the realtime voice service did not read it: "+fetchFailure(err)
	}
	sp.logEnd(fmt.Sprintf("failed, %d %s (%s)", status, code, fetchFailure(err)))
	speakError(w, status, code, msg)
}

func (sp *speech) failedCode(w http.ResponseWriter, status int, code, msg string) {
	sp.logEnd(fmt.Sprintf("failed, %d %s", status, code))
	speakError(w, status, code, msg)
}

// logEnd is the read-aloud's one line: which upstream, model and voice, how long the text was, how it went, how much
// audio came. Never the text.
func (sp *speech) logEnd(outcome string) {
	voice := firstNonEmpty(sp.voice, "default")
	sp.pl.logf("speak %d: %s/%s, voice %s, %d characters: %s; %d bytes at %d Hz in %v", sp.id,
		firstNonEmpty(sp.provider, "-"), firstNonEmpty(sp.model, "-"), voice, sp.runes, outcome, sp.bytes, sp.rate,
		logDur(time.Since(sp.began)))
}

func speakError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	writeHTTPJSON(w, status, map[string]any{"error": msg, "code": code})
}
