package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
)

type tweetThreader struct {
	t         tweeter
	inReplyTo int64
	initial   string
}

func (t *tweetThreader) tweetThread(ctx context.Context, tws []tweet) ([]int64, error) {
	inReplyTo := t.inReplyTo

	if t.initial != "" {
		initial := tweet{
			text: t.initial,
		}
		tws = append([]tweet{initial}, tws...)
	}

	ids := make([]int64, len(tws))
	for i, tw := range tws {
		tw.inReplyTo = inReplyTo

		id, err := t.t.tweet(ctx, tw)
		if err != nil {
			return nil, err
		}

		fmt.Println("tweeted", id)
		ids[i] = id
		inReplyTo = id
	}

	return ids, nil
}

type twitterTweeter struct {
	tc         *twitter.Client
	hc         *http.Client
	screenName string
}

func newTwitterTweeter(consumerKey, consumerSecret, appToken, appSecret string) (*twitterTweeter, error) {
	oaConfig := oauth1.NewConfig(consumerKey, consumerSecret)
	oaToken := oauth1.NewToken(appToken, appSecret)
	cl := oaConfig.Client(oauth1.NoContext, oaToken)
	twc := twitter.NewClient(cl)

	currentUser, _, err := twc.Accounts.VerifyCredentials(&twitter.AccountVerifyParams{
		IncludeEntities: twitter.Bool(false),
		SkipStatus:      twitter.Bool(true),
	})
	if err != nil {
		return nil, err
	}

	return &twitterTweeter{
		tc:         twc,
		hc:         cl,
		screenName: currentUser.ScreenName,
	}, nil
}

type tweetMedia struct {
	r       io.Reader
	altText string
}

type tweet struct {
	inReplyTo int64
	text      string

	media []tweetMedia
}

func (t *twitterTweeter) tweet(ctx context.Context, tw tweet) (int64, error) {
	var mediaIDs []int64
	for _, m := range tw.media {
		id, err := t.uploadMedia(m)
		if err != nil {
			return 0, fmt.Errorf("uploading media: %w", err)
		}
		mediaIDs = append(mediaIDs, id)
	}

	params := &twitter.StatusUpdateParams{
		MediaIds:          mediaIDs,
		InReplyToStatusID: tw.inReplyTo,
	}

	if tw.inReplyTo != 0 {
		tw.text = "@" + t.screenName + " " + tw.text
	}

	res, _, err := t.tc.Statuses.Update(tw.text, params)
	if err != nil {
		return 0, err
	}
	return res.ID, nil
}

func (t *twitterTweeter) uploadMedia(med tweetMedia) (int64, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	fw, err := w.CreateFormField("media")
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(fw, med.r); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}

	req, err := http.NewRequest("POST", "https://upload.twitter.com/1.1/media/upload.json", &b)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading upload body: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		bs := string(rb)
		if len(bs) > 200 {
			bs = bs[:200]
		}

		return 0, fmt.Errorf("got upload status %d: %q", resp.StatusCode, bs)
	}

	var mresp struct {
		MediaID int64 `json:"media_id"`
	}
	if err := json.Unmarshal(rb, &mresp); err != nil {
		return 0, err
	}

	if med.altText == "" {
		return mresp.MediaID, nil
	}

	var reqb struct {
		MediaID string `json:"media_id"`
		AltText struct {
			Text string `json:"text"`
		} `json:"alt_text"`
	}
	reqb.MediaID = strconv.FormatInt(mresp.MediaID, 10)
	reqb.AltText.Text = med.altText

	mrb, err := json.Marshal(reqb)
	if err != nil {
		return 0, err
	}

	req, err = http.NewRequest("POST", "https://upload.twitter.com/1.1/media/metadata/create.json", bytes.NewReader(mrb))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err = t.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	rb, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading metadata body: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		bs := string(rb)
		if len(bs) > 200 {
			bs = bs[:200]
		}

		return 0, fmt.Errorf("got metadata status %d: %q", resp.StatusCode, bs)
	}

	return mresp.MediaID, nil
}

type saveTweeter struct {
	mu sync.Mutex
	id int64
}

func (s *saveTweeter) tweet(ctx context.Context, tw tweet) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.id++ // avoid 0
	id := s.id

	prefix := fmt.Sprintf("tweet-%d", id)

	if tw.inReplyTo != 0 {
		tw.text = "in reply to " + strconv.FormatInt(tw.inReplyTo, 10) + ": " + tw.text
	}

	if err := os.WriteFile(prefix+".txt", []byte(tw.text), 0644); err != nil {
		return 0, err
	}

	for mi, m := range tw.media {
		mf, err := os.Create(fmt.Sprintf("%s-media-%d.png", prefix, mi))
		if err != nil {
			return 0, err
		}
		defer mf.Close()

		if _, err := io.Copy(mf, m.r); err != nil {
			return 0, err
		}

		if err := os.WriteFile(fmt.Sprintf("%s-media-%d.txt", prefix, mi), []byte(m.altText), 0644); err != nil {
			return 0, err
		}
	}

	return id, nil
}
