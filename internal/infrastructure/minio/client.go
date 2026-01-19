package minio

import (
	"context"
	"fmt"
	"io"
	"log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)
type MinIOClient struct {
	client *minio.Client
	bucket string
}
func NewMinIOClient(endpoint, bucket,accessKey,secretKey string,useSSL bool) (*MinIOClient,error) {
	//create minio client
	client,err := minio.New(endpoint,&minio.Options{
		Creds: credentials.NewStaticV4(accessKey,secretKey,""),
		Secure:useSSL,
	})
	if err != nil {
		return nil,fmt.Errorf("create client error by %s",err)
	}
	//check bucket
	ctx := context.Background()
	exists,err := client.BucketExists(ctx,bucket)
	if err != nil {
		return nil,fmt.Errorf("check bucket exists error: %w",err)
	}
	if !exists {
		err = client.MakeBucket(ctx,bucket,minio.MakeBucketOptions{})
		if err != nil {
			return nil,fmt.Errorf("create bucket error: %w",err)
		}
	}
	return &MinIOClient{client: client,bucket: bucket},nil
}
func (m *MinIOClient) PutObject(ctx context.Context,objectKey string, reader io.Reader,size int64,contentType string) error {
	info,err := m.client.PutObject(ctx,m.bucket,objectKey,reader,size,minio.PutObjectOptions{
		ContentType: contentType,
		PartSize: 5 * 1024 * 1024,
	})
	log.Printf("[MinIO] Uploaded: %s, Size: %d", objectKey, info.Size)
	return err
	

}