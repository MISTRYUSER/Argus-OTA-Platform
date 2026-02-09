// Mock Python Worker - 模拟 Python 数据聚合 Worker
// 功能：
// 1. 订阅 Kafka topic（batch-events）
// 2. 消费 AllFilesScattered 事件
// 3. 模拟数据聚合（sleep 1 秒）
// 4. 发布 GatheringCompleted 事件
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IBM/sarama"
)

func main() {
	log.Println("=== Mock Python Worker - Data Aggregation Simulator ===")

	brokers := []string{getEnv("KAFKA_BROKERS", "localhost:9092")}
	inputTopic := getEnv("KAFKA_INPUT_TOPIC", "all-files-scattered")
	outputTopic := getEnv("KAFKA_OUTPUT_TOPIC", "gathering-completed")
	groupID := getEnv("KAFKA_GROUP_ID", "mock-python-worker-group")

	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

	consumerGroup, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		log.Fatalf("Failed to create consumer group: %v", err)
	}
	defer consumerGroup.Close()

	handler := &ConsumerHandler{
		outputTopic: outputTopic,
		producer:    createProducer(brokers),
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			if err := consumerGroup.Consume(ctx, []string{inputTopic}, handler); err != nil {
				log.Printf("Consumer error: %v", err)
			}
		}
	}()

	log.Printf("Consuming from topic: %s", inputTopic)
	<-sigchan
	log.Println("Shutting down...")
	cancel()
	time.Sleep(1 * time.Second)
}

func createProducer(brokers []string) sarama.SyncProducer {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer(brokers, config)
	if err != nil {
		log.Fatalf("Failed to create producer: %v", err)
	}
	return producer
}

type ConsumerHandler struct {
	outputTopic string
	producer    sarama.SyncProducer
}

func (h *ConsumerHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *ConsumerHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *ConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		log.Printf("Received message from topic %s, partition %d, offset %d",
			msg.Topic, msg.Partition, msg.Offset)

		// 解析事件
		var event map[string]interface{}
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			log.Printf("Failed to unmarshal message: %v", err)
			continue
		}

		batchID, _ := event["batch_id"].(string)
		log.Printf("Processing batch: %s", batchID)

		// 模拟数据聚合
		time.Sleep(1 * time.Second)

		// 发布 GatheringCompleted 事件
		completedEvent := map[string]interface{}{
			"event_type": "GatheringCompleted",
			"batch_id":   batchID,
			"timestamp":  time.Now().Format(time.RFC3339),
		}

		data, _ := json.Marshal(completedEvent)
		_, _, err := h.producer.SendMessage(&sarama.ProducerMessage{
			Topic: h.outputTopic,
			Key:   sarama.StringEncoder(batchID),
			Value: sarama.ByteEncoder(data),
		})
		if err != nil {
			log.Printf("Failed to send message: %v", err)
		} else {
			log.Printf("Published GatheringCompleted for batch: %s", batchID)
		}

		session.MarkMessage(msg, "")
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
