package main

import (
	"flag"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
  "github.com/rs/zerolog/log"
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
	portPtr   = flag.Int("p", 8080, "port number as an int")
	loglevelPtr = flag.String("loglevel", "info", "Define the loglevel: debug,info,warning,error")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	filename  string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

func readFileIfModified(lastMod time.Time, lastPos int64) ([]byte, time.Time, int64, error) {
	log.Debug().
		Int64("lastMod", lastMod.Unix()).
		Int64("lastPos", lastPos).
		Msg("Called")
	fi, err := os.Stat(filename)
	if err != nil {
		log.Error().
			Err(err).
			Str("filename", filename).
			Msg("Stat ERROR")
		return nil, lastMod, lastPos, err
	}
	if !fi.ModTime().After(lastMod) {
		log.Debug().
			Msg("lastMod>ModTime")
		return nil, lastMod, lastPos, nil
	}
	f, err := os.Open(filename)
	if err != nil {
		log.Error().
			Err(err).
			Str("filename", filename).
			Msg("Open ERROR")
		return nil, lastMod, lastPos, nil
	}
	lastPos, err = f.Seek(lastPos, 0)
	if err != nil {
		log.Error().
			Err(err).
			Int64("lastPos", lastPos).
			Msg("Seek Error")
		lastPos, err = f.Seek(0, 0)
	}
	log.Debug().
		Int64("fileSize", fi.Size()).
		Int64("curPos", lastPos).
		Msg("file Current position")
	p := []byte("")
	size2read := fi.Size() - lastPos
	if size2read <= 0 {
		size2read = 0
		log.Debug().
			Int64("fileSize", fi.Size()).
			Int64("lastPos", lastPos).
			Int64("size2read", size2read).
			Msg("size2read<0")
	} else {
		p = make([]byte, size2read)
		count, err := f.Read(p)
		if err != nil {
			log.Error().
				Err(err).
				Int64("size2read", size2read).
				Msg("Read ERROR")
			return nil, fi.ModTime(), lastPos, err
		}
		log.Debug().
			Int64("fileSize", fi.Size()).
			Int64("lastPos", lastPos).
			Int64("size2read", size2read).
			Int("byteReadCount", count).
			Int("data_length", len(p)).
			Msg("File READ")
	}
	curPos, err := f.Seek(0,1)
	if err != nil {
		log.Error().
			Err(err).
			Msg("get last file pos Error")
		return nil, fi.ModTime(), 0, err
	}
	f.Close()
	log.Debug().
		Int64("ret_lastMod", fi.ModTime().Unix()).
		Int64("ret_lastPos", curPos).
		Int64("byte_count", size2read).
		Int("Data_length", len(string(p))).
		Msg("Returned data")
	return p, fi.ModTime(), curPos, nil
}

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		mtype, msg, err := ws.ReadMessage()
		if err != nil {
			log.Error().
				Err(err).
				Msg("reader.ReadMessage ERROR")
			break
		}
		log.Info().
			Int("MessageType", mtype).
			Str("Message", string(msg)).
			Msg("reader.ReadMessage")
	}
}

func writer(ws *websocket.Conn, lastMod time.Time, lastPos int64) {
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

			p, lastMod, lastPos, err = readFileIfModified(lastMod, lastPos)

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

func logClientIP(r *http.Request) {
	IPAddress := r.Header.Get("X-Real-Ip")
  if IPAddress == "" {
      IPAddress = r.Header.Get("X-Forwarded-For")
  }
  if IPAddress == "" {
      IPAddress = r.RemoteAddr
  }
	ip, port, err := net.SplitHostPort(IPAddress)
  if err != nil {
			log.Error().
				Err(err).
				Msgf("userip: %q is not IP:port", IPAddress)
  }

  userIP := net.ParseIP(ip)
  if userIP == nil {
			log.Error().
				Msgf("userip: %q is not IP:port", IPAddress)
      return
  }
	userIPstr := userIP.String()
	log.Info().
		Str("ip", userIPstr).
		Str("port", port).
		Msg("Connected from")
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	logClientIP(r)

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Error().
				Err(err).
				Msg("Upgrade ERROR")
		}
		return
	}

	var lastMod time.Time
	if n, err := strconv.ParseInt(r.FormValue("lastMod"), 10, 64); err == nil {
		lastMod = time.Unix(n, 0)
	}

	var lastPos int64
	if n, err := strconv.ParseInt(r.FormValue("lastPos"), 10, 64); err == nil {
		lastPos = n
	}

	go writer(ws, lastMod, lastPos)
	reader(ws)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	logClientIP(r)

	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p, lastMod, lastPos, err := readFileIfModified(time.Time{}, 0)
	if err != nil {
		log.Error().
			Err(err).
			Msg("serverHome.readFileIfModified ERROR")
		p = []byte(err.Error())
		lastMod = time.Unix(0, 0)
		lastPos = 0
	}
	log.Debug().
		Int("Data_length", len(string(p))).
		Int64("lastMod", lastMod.Unix()).
		Int64("lastPos", lastPos).
		Msg("serverHome.readFileIfModified")
	var v = struct {
		Host     string
		Data     string
		LastMod  string
		LastPos  string
		Filename string
	}{
		r.Host + r.URL.Path,
		string(p),
		strconv.FormatInt(lastMod.Unix(), 10),
		strconv.FormatInt(lastPos, 10),
		filename,
	}
	homeTempl.Execute(w, &v)
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		// fmt.Print("Usage: frontail [-p 8080] /path/filename\n\n")
		// fmt.Print("-p	listen port	8080\n\n")
		os.Exit(1)
	}
	filename = flag.Args()[0]

	switch *loglevelPtr {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warning":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		flag.Usage()
		os.Exit(1)
	}

	log.Info().
		Str("loglevel", *loglevelPtr).
		Msg("frontail started")

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", serveWs)
	if err := http.ListenAndServe(":"+strconv.Itoa(*portPtr), nil); err != nil {
		log.Fatal().
			Err(err).
			Msg("ListenAndServe ERROR")
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

			var conn = new WebSocket("ws://{{.Host}}ws?lastMod={{.LastMod}}&lastPos={{.LastPos}}");
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
