package js

import (
	"encoding/json"
	"fmt"
	"github.com/mailgun/vulcan/command"
	"github.com/mailgun/vulcan/netutils"
	"net/http"
)

func errorToJs(inErr error) map[string]interface{} {
	switch err := inErr.(type) {
	case *command.RetryError:
		return map[string]interface{}{
			"type":          "retry",
			"retry-seconds": err.Seconds,
			"code":          429,
			"message":       "Too Many Requests",
			"body":          "Too Many Requests",
		}
	default:
		return map[string]interface{}{
			"type":    "internal",
			"code":    http.StatusInternalServerError,
			"body":    http.StatusText(http.StatusInternalServerError),
			"message": err.Error(),
		}
	}
}

func errorFromJs(inErr interface{}) (*netutils.HttpError, error) {
	switch err := inErr.(type) {
	case map[string]interface{}:
		return errorFromDict(err)
	default:
		return nil, fmt.Errorf("Unsupported error type")
	}
}

func errorFromDict(in map[string]interface{}) (*netutils.HttpError, error) {
	codeI, ok := in["code"]
	if !ok {
		return nil, fmt.Errorf("Expected 'code' parameter")
	}
	codeF, ok := codeI.(float64)
	if !ok || codeF != float64(int(codeF)) {
		return nil, fmt.Errorf("Parameter 'code' should be integer")
	}
	message := http.StatusText(int(codeF))
	messageI, ok := in["message"]
	if ok {
		message, ok = messageI.(string)
		if !ok {
			return nil, fmt.Errorf("Parameter 'message' should be a string")
		}
	}
	bodyI, ok := in["body"]
	if !ok {
		return nil, fmt.Errorf("Expected 'body' parameter")
	}
	bodyBytes, err := json.Marshal(bodyI)
	if err != nil {
		return nil, fmt.Errorf("Failed to serialize body to json: %s", err)
	}

	return &netutils.HttpError{StatusCode: int(codeF), Status: message, Body: bodyBytes}, nil
}