package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/dghubble/oauth1"
	"github.com/graxinc/errutil"
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
			return nil, errutil.With(err)
		}

		fmt.Println("tweeted", id)
		ids[i] = id
		inReplyTo = id
	}

	return ids, nil
}

type multiTweetThreader []tweetThreader

func (m multiTweetThreader) tweetThread(ctx context.Context, tws []tweet) ([]string, error) {
	var errs []error
	var ids []string
	for _, t := range m {
		is, err := t.tweetThread(ctx, tws)
		if err != nil {
			errs = append(errs, errutil.With(err))
			continue
		}
		ids = append(ids, is...)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return ids, nil
}

type twitterTweeter struct {
	hc       *http.Client
	username string
}

func newTwitterTweeter(consumerKey, consumerSecret, appToken, appSecret string) (twitterTweeter, error) {
	oaConfig := oauth1.NewConfig(consumerKey, consumerSecret)
	oaToken := oauth1.NewToken(appToken, appSecret)
	cl := oaConfig.Client(context.Background(), oaToken)

	currentUser, err := currentTwitterUser(cl)
	if err != nil {
		return twitterTweeter{}, errutil.With(err)
	}

	return twitterTweeter{hc: cl, username: currentUser}, nil
}

func currentTwitterUser(cl *http.Client) (string, error) {
	resp, err := cl.Get("https://api.twitter.com/2/users/me")
	if err != nil {
		return "", errutil.With(err)
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			Username string
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", errutil.With(err)
	}
	return body.Data.Username, nil
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
	var mediaIDs []string
	for _, m := range tw.media {
		id, err := t.uploadMedia(m)
		if err != nil {
			return "", errutil.With(err)
		}
		mediaIDs = append(mediaIDs, id)
	}

	type Reply struct {
		ID string `json:"in_reply_to_tweet_id"`
	}
	type Media struct {
		IDs []string `json:"media_ids"`
	}
	var reqb struct {
		Text  string `json:"text"`
		Reply *Reply `json:"reply,omitempty"`
		Media *Media `json:"media,omitempty"`
	}
	reqb.Text = tw.text
	if tw.inReplyTo != "" {
		reqb.Reply = &Reply{ID: tw.inReplyTo}
	}
	if len(mediaIDs) > 0 {
		reqb.Media = &Media{IDs: mediaIDs}
	}

	b, err := json.Marshal(reqb)
	if err != nil {
		return "", errutil.With(err)
	}

	resp, err := t.hc.Post("https://api.twitter.com/2/tweets", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", errutil.With(err)
	}
	defer resp.Body.Close()

	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errutil.With(err)
	}

	if resp.StatusCode/100 != 2 {
		bs := string(rb)
		if len(bs) > 200 {
			bs = bs[:200]
		}

		return "", errutil.New(errutil.Tags{"code": resp.StatusCode, "bodySample": bs})
	}

	var respb struct {
		Data struct {
			ID string
		}
	}
	if err := json.Unmarshal(rb, &respb); err != nil {
		return "", errutil.With(err)
	}
	return respb.Data.ID, nil
}

func (t twitterTweeter) uploadMedia(med tweetMedia) (string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	fw, err := w.CreateFormField("media")
	if err != nil {
		return "", errutil.With(err)
	}
	if _, err := fw.Write(med.b); err != nil {
		return "", errutil.With(err)
	}
	if err := w.Close(); err != nil {
		return "", errutil.With(err)
	}

	req, err := http.NewRequest("POST", "https://upload.twitter.com/1.1/media/upload.json", &b)
	if err != nil {
		return "", errutil.With(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := t.hc.Do(req)
	if err != nil {
		return "", errutil.With(err)
	}
	defer resp.Body.Close()

	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errutil.With(err)
	}

	if resp.StatusCode/100 != 2 {
		bs := string(rb)
		if len(bs) > 200 {
			bs = bs[:200]
		}

		return "", errutil.New(errutil.Tags{"code": resp.StatusCode, "bodySample": bs})
	}

	var mresp struct {
		MediaID string `json:"media_id_string"`
	}
	if err := json.Unmarshal(rb, &mresp); err != nil {
		return "", errutil.With(err)
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
	reqb.MediaID = mresp.MediaID
	reqb.AltText.Text = med.altText

	mrb, err := json.Marshal(reqb)
	if err != nil {
		return "", errutil.With(err)
	}

	req, err = http.NewRequest("POST", "https://upload.twitter.com/1.1/media/metadata/create.json", bytes.NewReader(mrb))
	if err != nil {
		return "", errutil.With(err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	resp, err = t.hc.Do(req)
	if err != nil {
		return "", errutil.With(err)
	}
	defer resp.Body.Close()

	rb, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", errutil.With(err)
	}

	if resp.StatusCode/100 != 2 {
		bs := string(rb)
		if len(bs) > 200 {
			bs = bs[:200]
		}

		return "", errutil.New(errutil.Tags{"code": resp.StatusCode, "bodySample": bs})
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
		return mastodonTooter{}, errutil.With(err)
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
			return "", errutil.With(err)
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
		return "", errutil.With(err)
	}

	return fmt.Sprint(st.ID), nil
}

type blueskyPoster struct {
	client *xrpc.Client
}

func newBlueskyPoster(clientHost, handle, password string) (blueskyPoster, error) {
	ctx := context.Background()

	xrpcc := &xrpc.Client{
		Client: util.RobustHTTPClient(),
		Host:   clientHost,
		Auth:   &xrpc.AuthInfo{Handle: handle},
	}

	auth, err := atproto.ServerCreateSession(ctx, xrpcc, &atproto.ServerCreateSession_Input{
		Identifier: xrpcc.Auth.Handle,
		Password:   password,
	})
	if err != nil {
		return blueskyPoster{}, errutil.With(err)
	}

	xrpcc.Auth.AccessJwt = auth.AccessJwt
	xrpcc.Auth.RefreshJwt = auth.RefreshJwt
	xrpcc.Auth.Did = auth.Did
	xrpcc.Auth.Handle = auth.Handle

	return blueskyPoster{client: xrpcc}, nil
}

func (b blueskyPoster) tweet(ctx context.Context, tw tweet) (string, error) {
	post := &bsky.FeedPost{
		CreatedAt: time.Now().Format(time.RFC3339),
		Text:      tw.text,
	}

	if len(tw.media) > 0 {
		post.Embed = &bsky.FeedPost_Embed{EmbedImages: &bsky.EmbedImages{}}
	}
	for _, m := range tw.media {
		resp, err := atproto.RepoUploadBlob(ctx, b.client, bytes.NewReader(m.b))
		if err != nil {
			return "", errutil.With(err)
		}
		post.Embed.EmbedImages.Images = append(post.Embed.EmbedImages.Images, &bsky.EmbedImages_Image{
			Alt: m.altText,
			Image: &lexutil.LexBlob{
				Ref:      resp.Blob.Ref,
				MimeType: "image/png",
				Size:     resp.Blob.Size,
			},
		})
	}

	resp, err := atproto.RepoCreateRecord(ctx, b.client, &atproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.post",
		Repo:       b.client.Auth.Did,
		Record:     &lexutil.LexiconTypeDecoder{Val: post},
	})
	if err != nil {
		return "", errutil.With(err)
	}

	ref := &atproto.RepoStrongRef{
		Cid: resp.Cid,
		Uri: resp.Uri,
	}
	refB, err := json.Marshal(ref)
	if err != nil {
		return "", errutil.With(err)
	}

	return string(refB), nil
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
		return "", errutil.With(err)
	}

	for mi, m := range tw.media {
		mf, err := os.Create(fmt.Sprintf("%s-media-%d.png", prefix, mi))
		if err != nil {
			return "", errutil.With(err)
		}
		defer mf.Close()

		if _, err := mf.Write(m.b); err != nil {
			return "", errutil.With(err)
		}

		if err := os.WriteFile(fmt.Sprintf("%s-media-%d.txt", prefix, mi), []byte(m.altText), 0600); err != nil {
			return "", errutil.With(err)
		}
	}

	return id, nil
}
