package main

import (
	"crypto/rand"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	. "github.com/liverecord/server/common"
	. "github.com/liverecord/server/common/common"
	. "github.com/liverecord/server/common/frame"
	. "github.com/liverecord/server/handlers"
	. "github.com/liverecord/server/model"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

var Db *gorm.DB
var Cfg *ServerConfig
var logger = logrus.New()

type Message struct {
	Email   string `json:"email"`
	Message string `json:"password"`
}

type LrClient struct {
	Conn *websocket.Conn
	User *User
}

var clients = make(SocketClientsMap) // connected clients
var broadcast = make(chan Message)   // broadcast channel
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func handleConnections(w http.ResponseWriter, r *http.Request) {

	logger.Debug("handleConnections")
	// Upgrade initial GET request to a websocket
	ws, err := upgrader.Upgrade(w, r, nil)
	jwt := r.FormValue("jwt")
	logger.WithFields(logrus.Fields{"JWT": jwt}).Info("Request")
	if err != nil {
		logger.WithError(err).Error("Cannot upgrade protocol")
	} else {
		// Make sure we close the connection when the function returns
		defer ws.Close()

		// Register our new client
		clients[ws] = true

		w := map[string]interface{}{"type": 0, "connected": true}
		ws.WriteJSON(w)

		// our registry
		var lr = AppContext{
			Db:      Db,
			Cfg:     Cfg,
			Logger:  logger,
			Ws:      ws,
			Clients: &clients,
		}

		if len(jwt) > 0 {
			lr.AuthorizeJWT(jwt)
		}

		for {
			// var msg Message
			var frame Frame
			// Read in a new message as JSON and map it to a Message object
			//messageType, p, err := ws.ReadMessage()
			err := ws.ReadJSON(&frame)

			//log.Printf("read error: %v, message: %d bytes %s", err, messageType, p)
			if err != nil {
				log.Print(err)
				delete(clients, ws)
				break
			} else {
				logrus.Printf("Frame: %v", frame)

				switch frame.Type {
				case PingFrame:
					ws.WriteJSON(Frame{
						Type: PingFrame,
						Data: "Pong",
					})

				case AuthFrame:
					lr.Login(frame)

				case JWTFrame:
					lr.AuthorizeJWT(frame.Data)

				case UserInfoFrame:
					lr.UserInfo(frame)

				case UserListFrame:
					lr.UsersList(frame)

				case UserUpdateFrame:
					lr.UserUpdate(frame)

				case CategoryListFrame:
					lr.CategoryList(frame)

				case TopicUpdateFrame:
					lr.TopicSave(frame)
				case TopicListFrame:
					lr.TopicList(frame)
				}

			}
			// Send the newly received message to the broadcast channel
			// broadcast <- msg
		}
	}
}
func handleOauth(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Location", "/")
}

func handleBroadcastMessages() {
	for {
		// Grab the next message from the broadcast channel
		msg := <-broadcast
		// Send it out to every client that is currently connected
		for client := range clients {
			err := client.WriteJSON(msg)

			if err != nil {
				log.Printf("error: %v", err)
				client.Close()
				delete(clients, client)
			}
		}
	}
}

func main() {
	var err error
	err = godotenv.Load()

	logger.Out = os.Stdout

	if err != nil {
		logger.Fatal("Error loading .env file")
	}
	// open db connection
	Db, err = gorm.Open(
		"mysql",
		Env("MYSQL_DSN", "root:123@tcp(127.0.0.1:3306)/liveRecord?charset=utf8&parseTime=True"))

	// configure web-server
	fs := http.FileServer(http.Dir(Env("DOCUMENT_ROOT", "public")))
	http.Handle("/", fs)
	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/api/oauth/", handleOauth)
	http.HandleFunc("/api/oauth/facebook/", handleOauth)

	go handleBroadcastMessages()

	if err == nil {
		defer Db.Close()
		if BoolEnv("DEBUG", "false") {
			Db.LogMode(true)
			Db.Debug()
		}
		Db.AutoMigrate(&ServerConfig{})
		Db.AutoMigrate(&User{})
		Db.AutoMigrate(&Topic{})
		Db.AutoMigrate(&Comment{})
		Db.AutoMigrate(&Category{})
		Db.AutoMigrate(&SocialProfile{})
		Db.AutoMigrate(&Role{})

		var configRecord ServerConfig
		Db.First(&configRecord)

		if configRecord.ID == 0 {
			configRecord.JwtSignature = make([]byte, 256)
			_, err = io.ReadFull(rand.Reader, configRecord.JwtSignature)
			logger.WithError(err)
			Db.Save(&configRecord)
		}

		Cfg = &configRecord

		addr := Env("LISTEN_ADDR", ":8000")
		logger.Printf("Listening on %s", addr)
		err := http.ListenAndServe(addr, nil)
		if err != nil {
			logger.WithError(err).Fatal("Can't bind address & port")
		}

	} else {
		logger.WithError(err).Fatal("Can't bind address & port")

		logger.Panic(err)
	}
}
