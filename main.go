package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write the file to the client.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second

	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Poll file for changes with this period.
	filePeriod = 1 * time.Second
)

var (
	portPtr   = flag.Int("p", 8080, "an int")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	filename  string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

func readFileIfModified(lastMod time.Time) ([]byte, time.Time, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, lastMod, err
	}
	if !fi.ModTime().After(lastMod) {
		return nil, lastMod, nil
	}
	p, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fi.ModTime(), err
	}
	return p, fi.ModTime(), nil
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func writer(ws *websocket.Conn, lastMod time.Time) {
	lastError := ""
	pingTicker := time.NewTicker(pingPeriod)
	fileTicker := time.NewTicker(filePeriod)
	defer func() {
		pingTicker.Stop()
		fileTicker.Stop()
		ws.Close()
	}()
	for {
		select {
		case <-fileTicker.C:
			var p []byte
			var err error

			p, lastMod, err = readFileIfModified(lastMod)

			if err != nil {
				if s := err.Error(); s != lastError {
					lastError = s
					p = []byte(lastError)
				}
			} else {
				lastError = ""
			}

			if p != nil {
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.TextMessage, p); err != nil {
					return
				}
			}
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Println(err)
		}
		return
	}

	var lastMod time.Time
	if n, err := strconv.ParseInt(r.FormValue("lastMod"), 16, 64); err == nil {
		lastMod = time.Unix(0, n)
	}

	go writer(ws, lastMod)
	reader(ws)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p, lastMod, err := readFileIfModified(time.Time{})
	if err != nil {
		p = []byte(err.Error())
		lastMod = time.Unix(0, 0)
	}
	var v = struct {
		Host     string
		Data     string
		LastMod  string
		Filename string
	}{
		r.Host + r.URL.Path,
		string(p),
		strconv.FormatInt(lastMod.UnixNano(), 16),
		filename,
	}
	homeTempl.Execute(w, &v)
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Print("Usage: frontail [-p 8080] /path/filename\n\n")
		// fmt.Print("-p	listen port	8080\n\n")
		os.Exit(1)
	}
	filename = flag.Args()[0]
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWs)
	if err := http.ListenAndServe(":"+strconv.Itoa(*portPtr), nil); err != nil {
		log.Fatal(err)
	}
}

const homeHTML = `<!DOCTYPE html>
<html lang="en">
	<head>
		<meta charset="UTF-8">
		<meta name="description" content="Frontail: tail follow streaming file to the browser">
		<meta name="author" content="Krishna Modi <github.com/krish512>">
		<link rel="icon" href="data:;base64,iVBORw0KGgo=">
		<title>{{.Filename}}</title>
		<style>
			body {
				margin: 0;
				padding: 0;
			}
			header {
				display: flex;
				position: fixed;
				top: 0;
				padding: 20px 0;
				width: 100vw;
				background-color: black;
				color: white;
				font-size: 20px;
				font-family: sans-serif;
				justify-content: space-between;
			}
			#fileData {
				margin-top: 80px;
				padding: 0;
			}
			.log {
				padding: 0 10px;
				margin: 2px 0;
				white-space: pre-wrap;
				color: black;
				font-size: 1em;
				border: 0;
				cursor: default;
			}
			.selected {
				background-color: #ffb2b0;
			}
		</style>
    </head>
	<body>
		<header>
			<div style="padding: 0 20px;">File: {{.Filename}}</div>
			<div style="padding: 0 20px;"><input placeholder="filter" size="20" onkeyup="fmt(input,this.value)"></div>
		</header>
        <div id="fileData"></div>
		<script type="text/javascript">
			function fmt(input, filter="") {
				var lines = input.split("\n");
				data.innerHTML= "";
				var ll = lines.length;
				for(var i=0; i<ll; i++)
				{
					var regex = new RegExp( filter, 'ig' );
					if(filter == "" || filter == undefined || lines[i].match(regex)) {
						var elem = document.createElement('div');
						elem.className = "log";
						elem.addEventListener('click', function click() {
							if (this.className.indexOf('selected') === -1) {
							  this.className = 'log selected';
							} else {
							  this.className = 'log';
							}
						});
						elem.textContent = lines[i];
						data.appendChild(elem);
					}
				}
				window.scrollTo(0,document.body.scrollHeight);
			}
			
			var input = {{.Data}};
			var data = document.getElementById("fileData");
			fmt(input);
			
			var conn = new WebSocket("ws://{{.Host}}ws?lastMod={{.LastMod}}");
			conn.onclose = function(evt) {
				data.textContent = 'Connection closed';
			}
			conn.onmessage = function(evt) {
				console.log('file updated');
				input = evt.data;
				fmt(input);
			}
        </script>
    </body>
</html>
`
