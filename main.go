package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

/**
Generic Payload format is:
{
	"event": {
		"name": "<EVENT_TYPE>",
		"params": <ACTION_PARAMS>,
		"separator": " "
	},
	"action": {

	}
}

- Action for wifi connect/disconnect
{
	"event": {
		"name": "wireless_status_update",
		"params": {
			"client_mac": "<MAC_ADDRESS>",
			"action": "<AP-STA-CONNECTED|AP-STA-DISCONNECTED>",
			"interface": "<INTERFACE>"
		}
	}
	"action": {
		"cmd": "optional shell script",
		"params": {
			"interval": <NUM>, // interval between reconnects to treat as ne connection
			"only_for": ["mac1", "mac2"]
		}
	}
}
*/

type Payload struct {
	Event  Event  `json:"event"`
	Action Action `json:"action"`
}
type Event struct {
	Name      string                 `json:"name"`
	Params    map[string]interface{} `json:"params"`
	Separator string                 `json:"separator"`
}
type Action struct {
	Cmd    *string                `json:"cmd,omitempty"`
	Params map[string]interface{} `json:"params"`
}

var wireless_client_last_connect = map[string]time.Time{}

func main() {
	fmt.Println("Starting web handler")
	http.HandleFunc("/", handler)
	http.ListenAndServe(":9200", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		writeError(w, 400, "Only POST requests with application/json Content-Type are allowed")
		return
	}
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		writeError(w, 500, "Failed to read request body")
		return
	}
	err = processBody(bodyBytes)
	if err != nil {
		writeError(w, 400, fmt.Sprintf("failed processing body: %s", err))
		return
	}

}

func processBody(body []byte) error {
	var payload Payload
	err := json.Unmarshal(body, &payload)
	if err != nil {
		return errors.Wrap(err, "failed to unmarshal body bytes")
	}
	if err = verifyPayload(payload); err != nil {
		return errors.Wrap(err, "invalid payload")
	}
	if err = processPayload(payload); err != nil {
		return errors.Wrap(err, "failed executing payload")
	}

	return nil
}

/**
Check if payload matches any of the supported payloads
*/
func verifyPayload(payload Payload) error {
	switch payload.Event.Name {
	case "wireless_status_update":
		var params = payload.Event.Params
		if _, ok := params["client_mac_address"]; !ok {
			return fmt.Errorf("mandatory key \"client_mac_address\" is missing from event params")
		}
		if action, ok := params["action"]; !ok || action != "AP-STA-CONNECTED" && action != "AP-STA-DISCONNECTED" {
			return fmt.Errorf("mandatory key \"action\" is missing or has invalid value: %+v", action)
		}
		return nil
	default:
		return fmt.Errorf("event name \"%s\" is not supported", payload.Event.Name)
	}
}

func processPayload(payload Payload) error {
	switch payload.Event.Name {
	case "wireless_status_update":
		var event_params = payload.Event.Params
		client_mac_address, ok := event_params["client_mac_address"]
		if !ok {
			return fmt.Errorf("mandatory key \"client_mac_address\" is missing from event params")
		}
		if action, ok := event_params["action"]; !ok || action != "AP-STA-CONNECTED" && action != "AP-STA-DISCONNECTED" {
			return fmt.Errorf("mandatory key \"action\" is missing or has invalid value: %s", action)
		}
		interval := 3600
		params := payload.Action.Params

		if client_interval, ok := params["interval"]; ok {
			if client_interval_int, err := strconv.Atoi(fmt.Sprintf("%v", client_interval)); err == nil {
				interval = client_interval_int
			} else {
				return errors.Wrapf(err, "interval is not a number")
			}
		}

		// Check if client has connected previously
		last_connect, ok := wireless_client_last_connect[client_mac_address.(string)]
		if !ok {
			wireless_client_last_connect[client_mac_address.(string)] = time.Now()
		}
		// TODO(viktorbarzin): that's hacky, fix later
		event_connected := event_params["action"] == "AP-STA-CONNECTED"
		// If client connected earlier than interval, execute action
		if time.Now().Unix()-last_connect.Unix() > int64(interval) || event_connected {
			wireless_client_last_connect[client_mac_address.(string)] = time.Now()

			should_execute := true
			only_for := []string{}
			if only_for_param, ok := params["only_for"]; ok {
				for _, v := range only_for_param.([]interface{}) {
					only_for = append(only_for, fmt.Sprintf("%v", v))
				}
			}
			// check if client_mac in only_for param
			if len(only_for) > 0 {
				should_execute = false
				for _, v := range only_for {
					if strings.EqualFold(v, client_mac_address.(string)) {
						should_execute = true
						break
					}
				}
			}
			// if there is action define, run it
			if should_execute {
				fmt.Printf("Params: %+v", params)
				action_script := payload.Action.Cmd
				if action_script != nil {
					cmd := exec.Command("/bin/sh", "-c", *action_script)
					outputBytes, _ := cmd.CombinedOutput()
					output := string(outputBytes)
					if exit_code := cmd.ProcessState.ExitCode(); exit_code != 0 {
						return fmt.Errorf("running command failed with code %d, output: %s", exit_code, output)
					}
					fmt.Printf("output: '%s'", output)
					return nil
				} else {
					print("no command")
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("event name \"%s\" is not supported", payload.Event.Name)
	}
}
func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	w.Write([]byte(msg))
}
