// 1. 订阅 Kafka topic（batch-events）
// 2. 消费 AllFilesScattered 事件
// 3. 模拟数据聚合（sleep 1 秒）
// 4. 发布 GatheringCompleted 事件
package main