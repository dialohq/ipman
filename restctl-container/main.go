package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

var (
	SWAN_CONF_PATH = "/etc/swanctl/swanctl.conf"
)

type WrongArgumentsError struct{}
func (w *WrongArgumentsError)Error() string{
	// TODO: 
	// change initiate and terminate to paths
	// e.g. /initiate/foo/ or smth like that
	return `Amount of arguments and/or their type is invalid.
Valid arguments:
	GET  /status
	GET  /config
	PUT  /config    {"config": "baz"}
	POST /initiate  {"connName": "foo"}
	POST /terminate {"connName": "bar"}
	POST /reload
	`
}

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func swanExec(args ...string) (*exec.Cmd){
	return exec.Command("swanctl", args...)
}

func getAction(path string, connName *string) (*exec.Cmd, error){
	if connName == nil{
		switch path{
			case "/status":
				return swanExec("--list-conns"), nil
			case "/reload":
				return swanExec("--load-all", "--file", "/etc/swanctl/swanctl.conf"), nil
			case "/config":
				return exec.Command("cat", "/etc/swanctl/swanctl.conf"), nil
			default:
				return nil, &WrongArgumentsError{}
		}
	}

	// TODO: secure this
	switch path{
		case "/init":
			return swanExec("-i", "-c", *connName), nil
		case "/terminate":
			return swanExec("-t", "-c", *connName), nil
		default:
			return nil, &WrongArgumentsError{}
	}
}

func runCommandHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		_ = fmt.Errorf("Error parsing form: %s", err)
	}

	var splitPath []string
	argsPassed := strings.Contains(r.URL.String(), "?")
	if argsPassed{
		splitPath = strings.Split(r.URL.String(), "?")
	} else {
		splitPath = []string{r.URL.String()} 
	}

	path := splitPath[0]
	var connName *string 
	if len(splitPath) == 1{
		connName = nil
	} else {
		connName = &r.Form["connName"][0]
	}

	var resp CommandResponse
	cmd, err := getAction(path, connName)
	if err != nil{
		resp.Error = err.Error()
	} else {
		output, err := cmd.CombinedOutput()
		resp.Output = string(output)
		if err != nil {
			resp.Error = err.Error()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func p0ng(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("p0ng"))
}

func main() {
	http.HandleFunc("/status", runCommandHandler)
	http.HandleFunc("/reload", runCommandHandler)
	http.HandleFunc("/init", runCommandHandler)
	http.HandleFunc("/terminate", runCommandHandler)
	http.HandleFunc("/p1ng", p0ng)

	log.Println("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
