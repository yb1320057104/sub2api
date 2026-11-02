package binancepay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BillField preserves provider number text, including IDs beyond JavaScript's
// safe integer range. Only explicitly listed transaction fields are exposed.
type BillField struct {
	Key      string      `json:"key"`
	Type     string      `json:"type"`
	Value    string      `json:"value"`
	Children []BillField `json:"children,omitempty"`
}

type Bill struct {
	ID          string      `json:"id"`
	Channel     string      `json:"channel"`
	Kind        string      `json:"kind"`
	Currency    string      `json:"currency"`
	Amount      string      `json:"amount"`
	Direction   string      `json:"direction"`
	Status      string      `json:"status"`
	Time        time.Time   `json:"time"`
	PayerUID    string      `json:"payer_uid,omitempty"`
	ReceiverUID string      `json:"receiver_uid,omitempty"`
	Fields      []BillField `json:"fields"`
}

type BillsPage struct {
	Items        []Bill `json:"items"`
	PossiblyMore bool   `json:"possibly_more"`
	Limit        int    `json:"limit"`
	Total        int    `json:"total"`
}

var billAmountPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

// Bills is an independent read-only view, not a source of settleable Receipts.
// It intentionally keeps outgoing transfers, other kinds/currencies, and the
// full original amount text. A full 100-item page is flagged as possibly more.
func (c *Client) Bills(ctx context.Context, apiKey, secret, accountUID string, from, to time.Time) (*BillsPage, error) {
	if err := validateCredentials(apiKey, secret); err != nil {
		return nil, err
	}
	if !validUID(accountUID) || from.IsZero() || from.UnixMilli() < 0 || !to.After(from) || to.Sub(from) > 89*24*time.Hour {
		return nil, errors.New("binance: invalid bill query interval or account")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	operation, finish, err := c.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	offset, err := operation.syncTime(ctx)
	if err != nil {
		return nil, err
	}
	end := to.UnixMilli()
	if now := time.Now().UnixMilli() + offset; end > now {
		end = now
	}
	if end < from.UnixMilli() {
		return nil, &RequestError{Stage: "time_sync", Reason: "provider_time_invalid"}
	}
	var response historyResponse
	params := url.Values{"startTime": {strconv.FormatInt(from.UnixMilli(), 10)}, "endTime": {strconv.FormatInt(end, 10)}, "limit": {"100"}}
	if err := operation.get(ctx, historyPath, params, apiKey, secret, offset, &response); err != nil {
		return nil, err
	}
	if response.Code != "000000" || !response.Success {
		return nil, &RequestError{Stage: "pay_history", Reason: "pay_rejected", HTTPStatus: http.StatusOK, ProviderCode: safeProviderCode(response.Code)}
	}
	page := &BillsPage{Items: make([]Bill, 0), Limit: 100, PossiblyMore: len(response.Data) >= 100}
	if len(response.Data) > 100 {
		return nil, &RequestError{Stage: "pay_history", Reason: "response_oversized", HTTPStatus: http.StatusOK}
	}
	for _, raw := range response.Data {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var data map[string]any
		if err := decoder.Decode(&data); err != nil {
			return nil, &RequestError{Stage: "pay_history", Reason: "response_invalid", HTTPStatus: http.StatusOK}
		}
		id, amount := billScalar(data["transactionId"]), billScalar(data["amount"])
		millis, err := strconv.ParseInt(billScalar(data["transactionTime"]), 10, 64)
		if err != nil || millis <= 0 || id == "" || len(id) > 160 || len(amount) > 128 || !billAmountPattern.MatchString(amount) {
			return nil, &RequestError{Stage: "pay_history", Reason: "response_invalid", HTTPStatus: http.StatusOK}
		}
		if millis < from.UnixMilli() || millis > end {
			continue
		}
		payer, receiver := billPartyUID(data["payerInfo"]), billPartyUID(data["receiverInfo"])
		direction := "unknown"
		// Pay documents the signed amount as the account's income/expense.
		// Party IDs are display metadata and cannot override that direction.
		if strings.Trim(strings.TrimPrefix(amount, "-"), "0.") != "" {
			if strings.HasPrefix(amount, "-") {
				direction = "outgoing"
			} else {
				direction = "incoming"
			}
		}
		fields := safeBillField("data", data, 0).Children
		page.Items = append(page.Items, Bill{ID: id, Channel: "pay", Kind: billScalar(data["orderType"]), Currency: billScalar(data["currency"]), Amount: amount, Direction: direction, Status: "recorded", Time: time.UnixMilli(millis).UTC(), PayerUID: payer, ReceiverUID: receiver, Fields: fields})
	}
	sort.SliceStable(page.Items, func(i, j int) bool { return page.Items[i].Time.After(page.Items[j].Time) })
	page.Total = len(page.Items)
	return page, nil
}

func billScalar(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func billPartyUID(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	uid := billScalar(object["binanceId"])
	if !validUID(uid) {
		return ""
	}
	return uid
}

var allowedBillFields = map[string]bool{
	"transactionId": true, "orderType": true, "transactionTime": true, "amount": true, "currency": true,
	"payerInfo": true, "receiverInfo": true, "walletType": true, "walletTypes": true, "fundsDetail": true, "walletAssetCost": true,
	"name": true, "type": true, "binanceId": true, "email": true, "accountId": true, "countryCode": true, "phoneNumber": true, "mobileCode": true,
	"extend": true, "institutionName": true, "cardNumber": true, "digitalWalletId": true, "coin": true, "asset": true,
}

func safeBillField(key string, value any, depth int) BillField {
	f := BillField{Key: key}
	if depth > 8 {
		f.Type = "null"
		f.Value = "null"
		return f
	}
	switch value := value.(type) {
	case map[string]any:
		f.Type = "object"
		keys := make([]string, 0, len(value))
		for childKey, childValue := range value {
			allowed := allowedBillFields[childKey]
			if key == "walletAssetCost" {
				amount, ok := childValue.(string)
				_, numericErr := strconv.ParseUint(childKey, 10, 64)
				allowed = ok && numericErr == nil && len(amount) <= 128 && billAmountPattern.MatchString(amount)
			}
			if allowed {
				keys = append(keys, childKey)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			f.Children = append(f.Children, safeBillField(key, value[key], depth+1))
		}
	case []any:
		f.Type = "array"
		for i, child := range value {
			if i >= 100 {
				break
			}
			f.Children = append(f.Children, safeBillField(strconv.Itoa(i), child, depth+1))
		}
	case json.Number:
		f.Type = "number"
		f.Value = value.String()
	case string:
		f.Type = "string"
		f.Value = value
		if len(f.Value) > 4096 {
			f.Value = f.Value[:4096]
		}
	case bool:
		f.Type = "boolean"
		f.Value = strconv.FormatBool(value)
	default:
		f.Type = "null"
		f.Value = "null"
	}
	return f
}
