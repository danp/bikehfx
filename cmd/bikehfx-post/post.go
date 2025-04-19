package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/graxinc/errutil"
	"github.com/mattn/go-mastodon"
)

type postMedia struct {
	b       []byte
	altText string
}

type post struct {
	inReplyTo string
	text      string

	media []postMedia
}

type posterThreader struct {
	p         poster
	inReplyTo string
	initial   string
}

func (t posterThreader) postThread(ctx context.Context, posts []post) ([]string, error) {
	inReplyTo := t.inReplyTo

	if t.initial != "" {
		initial := post{
			text: t.initial,
		}
		posts = append([]post{initial}, posts...)
	}

	ids := make([]string, len(posts))
	for i, p := range posts {
		p.inReplyTo = inReplyTo

		id, err := t.p.post(ctx, p)
		if err != nil {
			return nil, errutil.With(err)
		}

		fmt.Println("posted", id)
		ids[i] = id
		inReplyTo = id
	}

	return ids, nil
}

type multiPosterThreader []posterThreader

func (m multiPosterThreader) postThread(ctx context.Context, posts []post) ([]string, error) {
	var errs []error
	var ids []string
	for _, p := range m {
		is, err := p.postThread(ctx, posts)
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

func (m mastodonTooter) post(ctx context.Context, p post) (string, error) {
	var mediaIDs []mastodon.ID
	for _, pm := range p.media {
		med := &mastodon.Media{
			File:        bytes.NewReader(pm.b),
			Description: pm.altText,
		}
		att, err := m.c.UploadMediaFromMedia(ctx, med)
		if err != nil {
			return "", errutil.With(err)
		}
		mediaIDs = append(mediaIDs, att.ID)
	}

	t := &mastodon.Toot{
		Status:      p.text,
		MediaIDs:    mediaIDs,
		InReplyToID: mastodon.ID(p.inReplyTo),
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

var bskyHashtagRegex = regexp.MustCompile(`#[a-zA-Z0-9_]+`)

func (b blueskyPoster) post(ctx context.Context, p post) (string, error) {
	post := &bsky.FeedPost{
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		Text:      p.text,
	}

	for _, matches := range bskyHashtagRegex.FindAllIndex([]byte(p.text), -1) {
		facet := &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(matches[0]),
				ByteEnd:   int64(matches[1]),
			},
			Features: []*bsky.RichtextFacet_Features_Elem{
				{
					RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
						Tag: p.text[matches[0]+1 : matches[1]], // +1 to skip the #
					},
				},
			},
		}
		post.Facets = append(post.Facets, facet)
	}

	if p.inReplyTo != "" {
		root, parent, err := b.resolveRef(ctx, p.inReplyTo)
		if err != nil {
			return "", errutil.With(err)
		}
		post.Reply = &bsky.FeedPost_ReplyRef{Root: root, Parent: parent}
	}

	if len(p.media) > 0 {
		post.Embed = &bsky.FeedPost_Embed{EmbedImages: &bsky.EmbedImages{}}
	}
	for _, m := range p.media {
		resp, err := atproto.RepoUploadBlob(ctx, b.client, bytes.NewReader(m.b))
		if err != nil {
			return "", errutil.With(err)
		}
		img := &bsky.EmbedImages_Image{
			Alt: m.altText,
			Image: &lexutil.LexBlob{
				Ref:      resp.Blob.Ref,
				MimeType: "image/png",
				Size:     resp.Blob.Size,
			},
		}

		decoded, err := png.Decode(bytes.NewReader(m.b))
		if err != nil {
			return "", errutil.With(err)
		}

		img.AspectRatio = &bsky.EmbedDefs_AspectRatio{
			Width:  int64(decoded.Bounds().Dx()),
			Height: int64(decoded.Bounds().Dy()),
		}
		post.Embed.EmbedImages.Images = append(post.Embed.EmbedImages.Images, img)
	}

	resp, err := atproto.RepoCreateRecord(ctx, b.client, &atproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.post",
		Repo:       b.client.Auth.Did,
		Record:     &lexutil.LexiconTypeDecoder{Val: post},
	})
	if err != nil {
		return "", errutil.With(err)
	}
	return resp.Uri, nil
}

func (b blueskyPoster) resolveRef(ctx context.Context, ref string) (root, parent *atproto.RepoStrongRef, _ error) {
	if len(ref) == 0 {
		return nil, nil, nil
	}

	// at:// uri to parent post (at://hfx.bike/app.bsky.feed.post/3lerr7lve2m22)
	// web url to parent post (https://bsky.app/profile/stats.hfx.bike/post/3lerr7lve2m22)

	if !strings.HasPrefix(ref, "at://") {
		u, err := url.Parse(ref)
		if err != nil {
			return nil, nil, errutil.With(err)
		}
		if u.Host != "bsky.app" {
			return nil, nil, errutil.New(errutil.Tags{"msg": "invalid host", "host": u.Host})
		}
		pathParts := strings.Split(u.Path, "/")
		if len(pathParts) != 5 {
			return nil, nil, errutil.New(errutil.Tags{"msg": "invalid path", "path": u.Path})
		}
		if pathParts[1] != "profile" && pathParts[3] != "post" {
			return nil, nil, errutil.New(errutil.Tags{"msg": "invalid path", "path": u.Path})
		}

		profile := pathParts[2]
		recordID := pathParts[4]

		ref = "at://" + profile + "/app.bsky.feed.post/" + recordID
	}

	resp, err := bsky.FeedGetPostThread(ctx, b.client, 1, 1, ref)
	if err != nil {
		return nil, nil, errutil.With(err)
	}

	post := resp.Thread.FeedDefs_ThreadViewPost.Post
	postRef := &atproto.RepoStrongRef{Cid: post.Cid, Uri: post.Uri}
	feedPost := post.Record.Val.(*bsky.FeedPost)
	if feedPost.Reply == nil {
		root = postRef
		parent = root
	} else {
		root = feedPost.Reply.Root
		parent = postRef
	}

	return root, parent, nil
}

type savePoster struct {
	mu sync.Mutex
	id int64
}

func (s *savePoster) post(ctx context.Context, p post) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.id++ // avoid 0
	id := fmt.Sprintf("%02d", s.id)

	prefix := fmt.Sprintf("post-%v.post", id)

	if err := os.WriteFile(prefix, []byte(p.text), 0600); err != nil {
		return "", errutil.With(err)
	}

	for mi, m := range p.media {
		imagePrefix := fmt.Sprintf("%s.image-%02d", prefix, mi)
		mf, err := os.Create(imagePrefix)
		if err != nil {
			return "", errutil.With(err)
		}
		defer mf.Close()

		if _, err := mf.Write(m.b); err != nil {
			return "", errutil.With(err)
		}

		imageAlt := fmt.Sprintf("%s.alt", imagePrefix)
		if err := os.WriteFile(imageAlt, []byte(m.altText), 0600); err != nil {
			return "", errutil.With(err)
		}
	}

	return id, nil
}
