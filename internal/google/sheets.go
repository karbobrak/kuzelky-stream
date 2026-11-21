package google

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
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
