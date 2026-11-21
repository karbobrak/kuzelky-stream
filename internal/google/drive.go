package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const folderMime = "application/vnd.google-apps.folder"

// DriveFile is a file in the app's backup folder.
type DriveFile struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Modified time.Time         `json:"modifiedTime"`
	Props    map[string]string `json:"appProperties"`
	Trashed  bool              `json:"trashed"`
}

func escQ(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) }

// EnsureFolder returns the id of the app's folder: the remembered one if it still exists, otherwise
// one found by name (with drive.file only folders this app created are visible), otherwise a new one.
func (c *Client) EnsureFolder(ctx context.Context, id, name string) (string, error) {
	if id != "" {
		var f DriveFile
		err := c.do(ctx, "GET", driveBase+"files/"+url.PathEscape(id)+"?fields=id,trashed", "", nil, &f)
		if err == nil && !f.Trashed {
			return id, nil
		}
		if err != nil && !isNotFound(err) {
			return "", err
		}
	}
	var list struct {
		Files []DriveFile `json:"files"`
	}
	q := url.Values{"q": {fmt.Sprintf("name='%s' and mimeType='%s' and trashed=false", escQ(name), folderMime)},
		"fields": {"files(id,name)"}, "spaces": {"drive"}}
	if err := c.do(ctx, "GET", driveBase+"files?"+q.Encode(), "", nil, &list); err != nil {
		return "", err
	}
	if len(list.Files) > 0 {
		return list.Files[0].ID, nil
	}
	b, _ := json.Marshal(map[string]any{"name": name, "mimeType": folderMime})
	var f DriveFile
	err := c.do(ctx, "POST", driveBase+"files?fields=id", "application/json", bytes.NewReader(b), &f)
	return f.ID, err
}

// ListFolder returns the files in the folder (not trashed).
func (c *Client) ListFolder(ctx context.Context, folderID string) ([]DriveFile, error) {
	var all []DriveFile
	token := ""
	for {
		q := url.Values{"q": {fmt.Sprintf("'%s' in parents and trashed=false", escQ(folderID))},
			"fields": {"nextPageToken,files(id,name,modifiedTime,appProperties)"}, "pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		var r struct {
			Files []DriveFile `json:"files"`
			Next  string      `json:"nextPageToken"`
		}
		if err := c.do(ctx, "GET", driveBase+"files?"+q.Encode(), "", nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Files...)
		if r.Next == "" {
			return all, nil
		}
		token = r.Next
	}
}

// Upload creates (existingID == "") or replaces a JSON file in the folder and returns its id.
// Metadata (name, appProperties) is sent every time, so the properties stay current.
func (c *Client) Upload(ctx context.Context, folderID, existingID, name string, data []byte, props map[string]string) (string, error) {
	multi := func(meta map[string]any) (*bytes.Buffer, string) {
		m, _ := json.Marshal(meta)
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		w, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
		w.Write(m)
		w, _ = mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json"}})
		w.Write(data)
		mw.Close()
		return &body, "multipart/related; boundary=" + mw.Boundary()
	}
	var f DriveFile
	if existingID != "" {
		body, ct := multi(map[string]any{"name": name, "appProperties": props})
		err := c.do(ctx, "PATCH", uploadBase+"files/"+url.PathEscape(existingID)+"?uploadType=multipart&fields=id", ct, body, &f)
		if err == nil {
			return f.ID, nil
		}
		if !isNotFound(err) {
			return "", err
		}
		// Deleted on Drive meanwhile: create it again.
	}
	body, ct := multi(map[string]any{"name": name, "parents": []string{folderID}, "mimeType": "application/json", "appProperties": props})
	err := c.do(ctx, "POST", uploadBase+"files?uploadType=multipart&fields=id", ct, body, &f)
	return f.ID, err
}

// Download returns a file's content.
func (c *Client) Download(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := c.do(ctx, "GET", driveBase+"files/"+url.PathEscape(id)+"?alt=media", "", nil, &data)
	return data, err
}

// DriveAccount returns the e-mail of the signed-in Drive account (shown in the settings).
func (c *Client) DriveAccount(ctx context.Context) (string, error) {
	var r struct {
		User struct {
			Email string `json:"emailAddress"`
			Name  string `json:"displayName"`
		} `json:"user"`
	}
	if err := c.do(ctx, "GET", driveBase+"about?fields=user(emailAddress,displayName)", "", nil, &r); err != nil {
		return "", err
	}
	if r.User.Email != "" {
		return r.User.Email, nil
	}
	return r.User.Name, nil
}

// MoveToFolder puts a file (e.g. the spreadsheet) into the app folder.
func (c *Client) MoveToFolder(ctx context.Context, fileID, folderID string) error {
	q := url.Values{"addParents": {folderID}, "removeParents": {"root"}, "fields": {"id"}}
	return c.do(ctx, "PATCH", driveBase+"files/"+url.PathEscape(fileID)+"?"+q.Encode(), "application/json",
		strings.NewReader("{}"), nil)
}
