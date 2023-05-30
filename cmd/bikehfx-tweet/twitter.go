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

	"github.com/dghubble/go-twitter/twitter" //nolint I know it's deprecated
	"github.com/dghubble/oauth1"
	"github.com/mattn/go-mastodon"
)

type tweetThreader struct {
	t         tweeter
	inReplyTo string
	initial   string
}

func (t tweetThreader) tweetThread(ctx context.Context, tws []tweet) ([]string, error) {
	inReplyTo := t.inReplyTo

	if t.initial != "" {
		initial := tweet{
			text: t.initial,
		}
		tws = append([]tweet{initial}, tws...)
	}

	ids := make([]string, len(tws))
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

type multiTweetThreader []tweetThreader

func (m multiTweetThreader) tweetThread(ctx context.Context, tws []tweet) ([]string, error) {
	var ids []string
	for _, t := range m {
		is, err := t.tweetThread(ctx, tws)
		if err != nil {
			return nil, err
		}
		ids = append(ids, is...)
	}
	return ids, nil
}

type twitterTweeter struct {
	tc         *twitter.Client
	hc         *http.Client
	screenName string
}

func newTwitterTweeter(consumerKey, consumerSecret, appToken, appSecret string) (twitterTweeter, error) {
	oaConfig := oauth1.NewConfig(consumerKey, consumerSecret)
	oaToken := oauth1.NewToken(appToken, appSecret)
	cl := oaConfig.Client(oauth1.NoContext, oaToken)
	twc := twitter.NewClient(cl)

	currentUser, _, err := twc.Accounts.VerifyCredentials(&twitter.AccountVerifyParams{
		IncludeEntities: twitter.Bool(false),
		SkipStatus:      twitter.Bool(true),
	})
	if err != nil {
		return twitterTweeter{}, err
	}

	return twitterTweeter{
		tc:         twc,
		hc:         cl,
		screenName: currentUser.ScreenName,
	}, nil
}

type tweetMedia struct {
	b       []byte
	altText string
}

type tweet struct {
	inReplyTo string
	text      string

	media []tweetMedia
}

func (t twitterTweeter) tweet(ctx context.Context, tw tweet) (string, error) {
	var mediaIDs []int64
	for _, m := range tw.media {
		id, err := t.uploadMedia(m)
		if err != nil {
			return "", fmt.Errorf("uploading media: %w", err)
		}
		mediaIDs = append(mediaIDs, id)
	}

	params := &twitter.StatusUpdateParams{
		MediaIds: mediaIDs,
	}
	if tw.inReplyTo != "" {
		n, err := strconv.ParseInt(tw.inReplyTo, 10, 64)
		if err != nil {
			return "", fmt.Errorf("bad inReplyTo %v", tw.inReplyTo)
		}
		params.InReplyToStatusID = n
		tw.text = "@" + t.screenName + " " + tw.text
	}

	res, _, err := t.tc.Statuses.Update(tw.text, params)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(res.ID), nil
}

func (t twitterTweeter) uploadMedia(med tweetMedia) (int64, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	fw, err := w.CreateFormField("media")
	if err != nil {
		return 0, err
	}
	if _, err := fw.Write(med.b); err != nil {
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

type mastodonTooter struct {
	c *mastodon.Client
}

// Requires read:accounts write:media write:statuses scopes.
func newMastodonTooter(server, clientID, clientSecret, accessToken string) (mastodonTooter, error) {
	cl := mastodon.NewClient(&mastodon.Config{
		Server:       server,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AccessToken:  accessToken,
	})

	_, err := cl.GetAccountCurrentUser(context.Background())
	if err != nil {
		return mastodonTooter{}, err
	}

	return mastodonTooter{cl}, nil
}

func (m mastodonTooter) tweet(ctx context.Context, tw tweet) (string, error) {
	var mediaIDs []mastodon.ID
	for _, tm := range tw.media {
		med := &mastodon.Media{
			File:        bytes.NewReader(tm.b),
			Description: tm.altText,
		}
		att, err := m.c.UploadMediaFromMedia(ctx, med)
		if err != nil {
			return "", err
		}
		mediaIDs = append(mediaIDs, att.ID)
	}

	t := &mastodon.Toot{
		Status:      tw.text,
		MediaIDs:    mediaIDs,
		InReplyToID: mastodon.ID(tw.inReplyTo),
		Visibility:  mastodon.VisibilityUnlisted,
	}

	st, err := m.c.PostStatus(ctx, t)
	if err != nil {
		return "", err
	}

	return fmt.Sprint(st.ID), nil
}

type saveTweeter struct {
	mu sync.Mutex
	id int64
}

func (s *saveTweeter) tweet(ctx context.Context, tw tweet) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.id++ // avoid 0
	id := fmt.Sprint(s.id)

	prefix := fmt.Sprintf("tweet-%v", id)

	if tw.inReplyTo != "" {
		tw.text = "in reply to " + tw.inReplyTo + ": " + tw.text
	}

	if err := os.WriteFile(prefix+".txt", []byte(tw.text), 0600); err != nil {
		return "", err
	}

	for mi, m := range tw.media {
		mf, err := os.Create(fmt.Sprintf("%s-media-%d.png", prefix, mi))
		if err != nil {
			return "", err
		}
		defer mf.Close()

		if _, err := mf.Write(m.b); err != nil {
			return "", err
		}

		if err := os.WriteFile(fmt.Sprintf("%s-media-%d.txt", prefix, mi), []byte(m.altText), 0600); err != nil {
			return "", err
		}
	}

	return id, nil
}
