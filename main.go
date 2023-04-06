package main

import (
	"context"
	"flag"
	"log"

	"open-ai-bots/utils"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type client struct{} // Add more data to this type if needed

var clients = make(map[*websocket.Conn]client) // Note: although large maps with pointer-like types (e.g. strings) as keys are slow, using pointers themselves as keys is acceptable and fast
var register = make(chan *websocket.Conn)
var broadcast = make(chan string)
var unregister = make(chan *websocket.Conn)

var (
	port = flag.String("port", getPort(), "Port to listen on")
	prod = flag.Bool("prod", false, "Enable prefork in Production")
)

func getPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = ":3001"
	} else {
		port = ":" + port
	}

	return port
}

func runHub() {
	for {
		select {
		case connection := <-register:
			clients[connection] = client{}
			log.Println("connection registered")

		case message := <-broadcast:
			log.Println("message received:", message)

			// Send the message to all clients
			for connection := range clients {
				if err := connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
					log.Println("write error:", err)

					unregister <- connection
					connection.WriteMessage(websocket.CloseMessage, []byte{})
					connection.Close()
				}
			}

		case connection := <-unregister:
			// Remove the client from the hub
			delete(clients, connection)

			log.Println("connection unregistered")
		}
	}
}

func main() {
	app := fiber.New(fiber.Config{
		Prefork: *prod, // go run app.go -prod
	})
	v1 := app.Group("/api/v1")

	v1.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello, World 👋!")
	})
	go runHub()

	app.Use("/ws", func(c *fiber.Ctx) error {
		// IsWebSocketUpgrade returns true if the client
		// requested upgrade to the WebSocket protocol.
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Get("/ws/:id", websocket.New(func(c *websocket.Conn) {
		// c.Locals is added to the *websocket.Conn
		log.Println(c.Locals("allowed"))  // true
		log.Println(c.Params("id"))       // 123
		log.Println(c.Query("v"))         // 1.0
		log.Println(c.Cookies("session")) // ""

		// websocket.Conn bindings https://pkg.go.dev/github.com/fasthttp/websocket?tab=doc#pkg-index
		defer func() {
			unregister <- c
			c.Close()
		}()

		// Register the client
		register <- c

		for {
			messageType, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Println("read error:", err)
				}

				return // Calls the deferred function, i.e. closes the connection on error
			}

			if messageType == websocket.TextMessage {
				// Broadcast the received message
				broadcast <- string(message)
			} else {
				log.Println("websocket message received of type", messageType)
			}
		}

	}))
	cli, err := utils.WAConnect()
	v1.Post("/", func(c *fiber.Ctx) error {
		payload := struct {
			Phone   string `json:"phone"`
			Message string `json:"message"`
		}{}

		if err := c.BodyParser(&payload); err != nil {
			return err
		}
		user, _ := types.ParseJID(payload.Phone + "@s.whatsapp.net")

		cli.SendMessage(context.Background(), user, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(payload.Message),
			},
		})
		return c.JSON(payload)
	})

	if err != nil {
		return
	}

	// Listen on port 3000
	log.Fatal(app.Listen(*port)) // go run app.go -port=:3000

}
