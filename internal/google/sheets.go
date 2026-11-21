package google

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
)

// CreateSpreadsheet creates a spreadsheet with the given sheet (tab) names; returns its id.
func (c *Client) CreateSpreadsheet(ctx context.Context, title string, sheets ...string) (string, error) {
	var tabs []map[string]any
	for _, s := range sheets {
		tabs = append(tabs, map[string]any{"properties": map[string]any{"title": s,
			"gridProperties": map[string]any{"frozenRowCount": 1}}})
	}
	b, _ := json.Marshal(map[string]any{"properties": map[string]any{"title": title, "locale": "cs_CZ"}, "sheets": tabs})
	var r struct {
		ID string `json:"spreadsheetId"`
	}
	err := c.do(ctx, "POST", sheetsBase+"spreadsheets", "application/json", bytes.NewReader(b), &r)
	return r.ID, err
}

// SpreadsheetExists reports whether the spreadsheet is still accessible.
func (c *Client) SpreadsheetExists(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	err := c.do(ctx, "GET", sheetsBase+"spreadsheets/"+url.PathEscape(id)+"?fields=spreadsheetId", "", nil, nil)
	if isNotFound(err) {
		return false, nil
	}
	if ae, ok := err.(*APIError); ok && ae.Status == 403 {
		return false, nil
	}
	return err == nil, err
}

// SpreadsheetURL is the link to open it in the browser.
func SpreadsheetURL(id string) string { return "https://docs.google.com/spreadsheets/d/" + id }

// GetColumn returns the values of one column range, e.g. "'Utkání'!A:A".
func (c *Client) GetColumn(ctx context.Context, id, rng string) ([]string, error) {
	var r struct {
		Values [][]string `json:"values"`
	}
	u := sheetsBase + "spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(rng) + "?majorDimension=COLUMNS"
	if err := c.do(ctx, "GET", u, "", nil, &r); err != nil {
		return nil, err
	}
	if len(r.Values) == 0 {
		return nil, nil
	}
	return r.Values[0], nil
}

// ValueRange is one block of cells to write.
type ValueRange struct {
	Range  string  `json:"range"`
	Values [][]any `json:"values"`
}

// BatchUpdate writes several ranges at once (values are parsed as if typed by a user).
func (c *Client) BatchUpdate(ctx context.Context, id string, data []ValueRange) error {
	if len(data) == 0 {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"valueInputOption": "USER_ENTERED", "data": data})
	return c.do(ctx, "POST", sheetsBase+"spreadsheets/"+url.PathEscape(id)+"/values:batchUpdate",
		"application/json", bytes.NewReader(b), nil)
}

// Append adds rows after the last row of the sheet.
func (c *Client) Append(ctx context.Context, id, rng string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"values": rows})
	u := sheetsBase + "spreadsheets/" + url.PathEscape(id) + "/values/" + url.PathEscape(rng) +
		":append?valueInputOption=USER_ENTERED&insertDataOption=INSERT_ROWS"
	return c.do(ctx, "POST", u, "application/json", bytes.NewReader(b), nil)
}

// SheetIDs returns the numeric id of each tab by its title (needed to delete rows).
func (c *Client) SheetIDs(ctx context.Context, id string) (map[string]int64, error) {
	var r struct {
		Sheets []struct {
			Properties struct {
				ID    int64  `json:"sheetId"`
				Title string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	u := sheetsBase + "spreadsheets/" + url.PathEscape(id) + "?fields=sheets.properties(sheetId,title)"
	if err := c.do(ctx, "GET", u, "", nil, &r); err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, s := range r.Sheets {
		out[s.Properties.Title] = s.Properties.ID
	}
	return out, nil
}

// DeleteRows removes whole rows (1-based numbers) from one tab.
func (c *Client) DeleteRows(ctx context.Context, id string, sheetID int64, rows []int) error {
	if len(rows) == 0 {
		return nil
	}
	rows = slices.Clone(rows)
	slices.Sort(rows)
	slices.Reverse(rows) // bottom up, so the remaining numbers stay valid
	var reqs []map[string]any
	for _, r := range rows {
		reqs = append(reqs, map[string]any{"deleteDimension": map[string]any{"range": map[string]any{
			"sheetId": sheetID, "dimension": "ROWS", "startIndex": r - 1, "endIndex": r}}})
	}
	b, _ := json.Marshal(map[string]any{"requests": reqs})
	return c.do(ctx, "POST", sheetsBase+"spreadsheets/"+url.PathEscape(id)+":batchUpdate",
		"application/json", bytes.NewReader(b), nil)
}
