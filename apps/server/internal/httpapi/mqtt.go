package httpapi

import (
	"encoding/json"
	"log"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"ali-mdm/server/internal/device"
)

// MQTTBridge subscribes to operator-pushed commands and feeds the PokeQueue.
// Topic: alimdm/poke/{deviceID}  payload: device.Poke JSON.
type MQTTBridge struct {
	queue *PokeQueue
}

func NewMQTTBridge(queue *PokeQueue) *MQTTBridge { return &MQTTBridge{queue: queue} }

func (b *MQTTBridge) Start(brokerURL string) error {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("alimdm-cloud-api").
		SetAutoReconnect(true)
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	subTok := client.Subscribe("alimdm/poke/#", 1, func(_ mqtt.Client, msg mqtt.Message) {
		var p device.Poke
		if json.Unmarshal(msg.Payload(), &p) != nil {
			log.Printf("mqtt: bad poke payload: %s", msg.Payload())
			return
		}
		// topic: alimdm/poke/{deviceID}
		devID := msg.Topic()
		const prefix = "alimdm/poke/"
		if len(devID) > len(prefix) {
			devID = devID[len(prefix):]
		}
		b.queue.Enqueue(devID, p)
		log.Printf("mqtt: enqueued poke for %s type=%s", devID, p.Type)
	})
	if subTok.Wait() && subTok.Error() != nil {
		return subTok.Error()
	}
	log.Printf("mqtt: connected to %s, subscribed alimdm/poke/#", brokerURL)
	return nil
}
