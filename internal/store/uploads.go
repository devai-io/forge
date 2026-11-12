package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

// Upload is an image attached to an agent prompt or message. The token is
// the only way to name it: it travels in the prompt's marker (UploadMarker)
// to the machine, which fetches the bytes with it.
type Upload struct {
	Token       string    `json:"token"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int       `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
	Marker      string    `json:"marker"`

	UserID int64  `json:"-"`
	Data   []byte `json:"-"`
}

// UploadMarker is how a prompt names an attached image.
func UploadMarker(name, token string) string {
	return fmt.Sprintf("[image: %s](forge-upload:%s)", name, token)
}

const uploadCols = `token, name, content_type, size, created_at, user_id`

func (s *Store) CreateUpload(ctx context.Context, userID int64, name, contentType string, data []byte) (*Upload, error) {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	u := Upload{Token: token, Name: name, ContentType: contentType, Size: len(data), UserID: userID}
	err := s.DB.QueryRow(ctx, `INSERT INTO uploads (token, user_id, name, content_type, size, data)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`, token, userID, name, contentType, len(data), data).Scan(&u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.Marker = UploadMarker(name, token)
	return &u, nil
}

// UploadByToken returns the upload with its bytes.
func (s *Store) UploadByToken(ctx context.Context, token string) (*Upload, error) {
	var u Upload
	var userID *int64
	err := s.DB.QueryRow(ctx, `SELECT `+uploadCols+`, data FROM uploads WHERE token = $1`, token).
		Scan(&u.Token, &u.Name, &u.ContentType, &u.Size, &u.CreatedAt, &userID, &u.Data)
	if err != nil {
		return nil, mapErr(err)
	}
	if userID != nil {
		u.UserID = *userID
	}
	u.Marker = UploadMarker(u.Name, u.Token)
	return &u, nil
}

// PruneUploads drops images older than maxAge: by then the agent that was
// shown them has long been done with them.
func (s *Store) PruneUploads(ctx context.Context, maxAge time.Duration) (int, error) {
	tag, err := s.DB.Exec(ctx, `DELETE FROM uploads WHERE created_at < ts_add(now(), -$1)`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
