package utils

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	firebase "firebase.google.com/go"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redis_rate/v9"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"
	whatsmeow "go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"golang.org/x/net/websocket"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

type socketData struct {
	Text string `json:"text"`
}

func deepCopy(s string) string {
	var sb strings.Builder
	sb.WriteString(s)
	return sb.String()
}

func WAConnect() (*whatsmeow.Client, error) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr:     "containers-us-west-162.railway.app:7442",
		Username: "default",
		Password: "hs53jTNK9qP665GBlA30",
	})
	_ = rdb.FlushDB(ctx).Err()

	limiter := redis_rate.NewLimiter(rdb)

	store.DeviceProps.RequireFullSync = proto.Bool(true)
	dbLog := waLog.Stdout("Database", "DEBUG", false)
	// Make sure you add appropriate DB connector imports, e.g. github.com/mattn/go-sqlite3 for SQLite
	container, err := sqlstore.New("sqlite3", "file:examplestore.db?_foreign_keys=on", dbLog)
	if err != nil {
		panic(err)
	}
	// If you want multiple sessions, remember their JIDs and use .GetDevice(jid) or .GetAllDevices() instead.
	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		panic(err)
	}

	opt := option.WithCredentialsFile("opengptwhatsapp-firebase-adminsdk-ew07x-09036a5918.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	clt, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}

	clientLog := waLog.Stdout("Client", "DEBUG", false)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	eventHandler := func(evt interface{}) {
		conn, err := websocket.Dial("ws://127.0.0.1:3001/ws/123?v=1.0", "", "http://127.0.0.1:3001")
		if err != nil {
			fmt.Print("Failed to connect to websocket")
		}
		switch v := evt.(type) {
		case *events.Message:
			message := deepCopy(v.Message.GetConversation())
			if err = websocket.JSON.Send(conn, message); err != nil {
				fmt.Print("Failed to connect to websocket")
			}
			res, err := limiter.Allow(ctx, v.Info.Sender.String(), redis_rate.Limit{Rate: 5, Burst: 5, Period: time.Hour * 1})
			if err != nil {
				panic(err)
			}

			fmt.Println("Received a message!", v.Info.Sender.String(), message)
			if v.Message.GetConversation() == "!ping" {
				client.SendMessage(context.Background(), v.Info.Sender, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String("pong"),
					},
				})
			} else if strings.HasPrefix(v.Message.GetConversation(), "!chat ") {

				if res.Remaining > 0 {
					reply := OpenAIBot(message[6:]) + "\n\n\n" + "developed by Zenith"
					ref := clt.Collection("messages").NewDoc()
					_, err := ref.Set(ctx, map[string]interface{}{
						"sender":  v.Info.Sender.User,
						"message": message[6:],
						"reply":   reply,
					})
					if err != nil {
						// Handle any errors in an appropriate way, such as returning them.
						fmt.Printf("An error has occurred: %s", err)
					}
					client.SendMessage(context.Background(), v.Info.Sender, &waProto.Message{
						ExtendedTextMessage: &waProto.ExtendedTextMessage{
							Text: proto.String(reply),
						},
					})

				} else {
					client.SendMessage(context.Background(), v.Info.Sender, &waProto.Message{
						ExtendedTextMessage: &waProto.ExtendedTextMessage{
							Text: proto.String("Too Many Requests In 1 Hour Try Again"),
						},
					})
				}

			}
		case *events.Disconnected:
			fmt.Println("Discounted")
		case *events.LoggedOut:
			fmt.Println("Log out")
			client.Disconnect()
			go func() {
				qrChan, _ := client.GetQRChannel(context.Background())
				err = client.Connect()
				if err != nil {
					panic(err)
				}

				for evt := range qrChan {
					if evt.Event == "code" {
						// Render the QR code here
						// e.g. qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
						// or just manually `echo 2@... | qrencode -t ansiutf8` in a terminal
						qrterminal.Generate(evt.Code, qrterminal.L, os.Stdout)
						fmt.Println("QR code:", evt.Code)
					} else {
						fmt.Println("Login event:", evt.Event)
					}
				}
			}()
		case *events.AppState:
			clientLog.Debugf("App state event: %+v / %+v", v.Index, v.SyncActionValue)
		case *events.KeepAliveTimeout:
			clientLog.Debugf("Keepalive timeout event: %+v", evt)
			if v.ErrorCount > 3 {
				clientLog.Debugf("Got >3 keepalive timeouts, forcing reconnect")
				go func() {
					client.Disconnect()
					err := client.Connect()
					if err != nil {
						clientLog.Errorf("Error force-reconnecting after keepalive timeouts: %v", err)
					}
				}()
			}
		case *events.KeepAliveRestored:
			clientLog.Debugf("Keepalive restored")

		}

	}
	client.AddEventHandler(eventHandler)

	if client.Store.ID == nil {
		// No ID stored, new login
		qrChan, _ := client.GetQRChannel(context.Background())

		err = client.Connect()
		if err != nil {
			panic(err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				// Render the QR code here
				// e.g. qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				// or just manually `echo 2@... | qrencode -t ansiutf8` in a terminal
				qrterminal.Generate(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("QR code:", evt.Code)
			} else {
				fmt.Println("Login event:", evt.Event)
			}
		}
	} else {
		// Already logged in, just connect
		err = client.Connect()
		if err != nil {
			panic(err)
		}
	}

	return client, nil
}
