package telegramgotd

import (
	"context"
	"io"
	"strconv"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive/core/telegram"
)

// Telegram precise upload.getFile rules: offset and limit are multiples of
// 1 KiB, limit is at most 1 MiB, and one request never crosses a 1 MiB
// boundary.
const (
	preciseAlign    = 1024
	preciseMaxChunk = 1024 * 1024
)

func (c *Client) ReadMediaRange(ctx context.Context, channelID int64, messageID int, offset, length int64, dst io.Writer) (tgtelegram.MediaInfo, error) {
	var info tgtelegram.MediaInfo
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		peer, err := c.resolveChannelPeer(ctx, api, strconv.FormatInt(channelID, 10))
		if err != nil {
			return err
		}
		msgs, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: peer.ChannelID, AccessHash: peer.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err != nil {
			return mapRPCError(err)
		}
		msg, err := firstMessage(msgs)
		if err != nil {
			return err
		}
		info, err = readMessageRange(ctx, api, msg, offset, length, dst)
		return err
	})
	return info, err
}

// readMessageRange describes msg's media representation and, for a non-empty
// valid interval, writes exactly bytes [offset, offset+length) of it to dst.
func readMessageRange(ctx context.Context, api *tg.Client, msg *tg.Message, offset, length int64, dst io.Writer) (tgtelegram.MediaInfo, error) {
	info, loc, err := mediaRangeSource(msg)
	if err != nil {
		return info, err
	}
	if err := tgtelegram.CheckMediaRange(info, offset, length); err != nil {
		return info, err
	}
	if length == 0 {
		return info, nil
	}
	return info, readPreciseRange(ctx, api, loc, offset, length, dst)
}

// mediaRangeSource reports the representation readMessageRange serves and,
// when it is seekable, the file location to read it from. Documents
// (including video documents) keep their uploaded bytes and exact size.
// Native photos serve Telegram's largest recompressed size, seekable only
// when Telegram states that size's exact length. Text messages have no file
// and are not seekable.
func mediaRangeSource(msg *tg.Message) (tgtelegram.MediaInfo, tg.InputFileLocationClass, error) {
	unknown := tgtelegram.MediaInfo{Size: -1}
	switch media := msg.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return unknown, nil, errors.New("message has no document")
		}
		info := tgtelegram.MediaInfo{Size: doc.Size, Seekable: true, MIME: doc.MimeType}
		return info, doc.AsInputDocumentFileLocation(""), nil
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return unknown, nil, errors.New("message has no photo")
		}
		thumb, size := largestPhotoSize(photo)
		if size < 0 {
			return tgtelegram.MediaInfo{Size: -1, MIME: "image/jpeg"}, nil, nil
		}
		info := tgtelegram.MediaInfo{Size: size, Seekable: true, MIME: "image/jpeg"}
		return info, photo.AsInputPhotoFileLocation(thumb), nil
	case nil:
		if msg.Message == "" {
			return unknown, nil, errors.New("message has no downloadable content")
		}
		return tgtelegram.MediaInfo{Size: -1, MIME: "text/plain; charset=utf-8"}, nil, nil
	default:
		return unknown, nil, errors.New("unsupported media type")
	}
}

// readPreciseRange writes bytes [offset, offset+length) of loc to dst using
// precise upload.getFile requests, trimming the alignment overfetch.
func readPreciseRange(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, offset, length int64, dst io.Writer) error {
	end := offset + length
	alignedEnd := (end + preciseAlign - 1) / preciseAlign * preciseAlign
	for pos := offset; pos < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := pos / preciseAlign * preciseAlign
		reqEnd := min((start/preciseMaxChunk+1)*preciseMaxChunk, alignedEnd)
		req := &tg.UploadGetFileRequest{Location: loc, Offset: start, Limit: int(reqEnd - start)}
		req.SetPrecise(true)
		res, err := api.UploadGetFile(ctx, req)
		if err != nil {
			return mapRPCError(err)
		}
		file, ok := res.(*tg.UploadFile)
		if !ok {
			return errors.Errorf("unexpected upload.getFile result %T", res)
		}
		skip := pos - start
		if int64(len(file.Bytes)) <= skip {
			return io.ErrUnexpectedEOF
		}
		chunk := file.Bytes[skip:]
		if want := min(reqEnd, end) - pos; int64(len(chunk)) > want {
			chunk = chunk[:want]
		}
		if _, err := dst.Write(chunk); err != nil {
			return err
		}
		pos += int64(len(chunk))
	}
	return nil
}
