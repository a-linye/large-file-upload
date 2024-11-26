package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"io"
	"os"
	"sort"
    "path"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MinIO配置
var (
	endpoint        = "172.22.121.29:9000" // MinIO服务地址
	accessKeyID     = "evaluation_backend" // Access Key
	secretAccessKey = "evaluation_backend" // Secret Key
	bucketName      = "upload-bucket"      // 存储桶名称
)

func main() {
	// 初始化MinIO客户端
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		log.Fatalf("Unable to initialize MinIO client: %v", err)
	}

	// 创建桶
	if err := createBucket(client); err != nil {
		log.Fatalf("Unable to create bucket: %v", err)
	}

	// 初始化Gin路由
	r := gin.Default()

	// 路由设置
	r.POST("/upload", handleFileUpload(client))
	r.GET("/upload-status", handleUploadStatus(client))
	r.POST("/merge", handleFileMerge(client))
	r.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})

	// 启动Gin服务器
	log.Println("Starting server at :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Unable to start server: ", err)
	}
}

// 创建桶
func createBucket(client *minio.Client) error {
	// 使用 context 调用 BucketExists 方法
	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucketName)
	if err != nil {
		return err
	}
	if !exists {
		err := client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{Region: "us-east-1"})
		if err != nil {
			return err
		}
	}
	return nil
}

// 处理文件上传
func handleFileUpload(client *minio.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取文件上传相关信息
		file, _, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unable to read file"})
			return
		}
		defer file.Close()

		// 获取文件名、分片信息
		fileName := c.DefaultPostForm("filename", "unknown")
		partNumber, _ := strconv.Atoi(c.DefaultPostForm("part_number", "0"))
		totalParts, _ := strconv.Atoi(c.DefaultPostForm("total_parts", "1"))
		uploadID := c.DefaultPostForm("upload_id", "")

		// 使用文件名和分片号生成分片文件名
		partFileName := fmt.Sprintf("%s/%s_part_%d", uploadID, fileName, partNumber)

		// 上传分片
		_, err = client.PutObject(c, bucketName, partFileName, file, -1, minio.PutObjectOptions{ContentType: "application/octet-stream"})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to upload part"})
			return
		}

		// 返回上传成功响应
		c.JSON(http.StatusOK, gin.H{
			"message": fmt.Sprintf("Part %d of %d uploaded successfully", partNumber, totalParts),
		})
	}
}

// 获取上传状态
func handleUploadStatus(client *minio.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取所有已上传的文件分片
		uploadedParts := make([]string, 0)

		// 通过 ListObjects 获取所有对象
		objectCh := client.ListObjects(c, bucketName, minio.ListObjectsOptions{
			Recursive: true,
		})

		// 从 channel 中读取已上传的对象
		for object := range objectCh {
			if object.Err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to list objects"})
				return
			}
			// 解析出上传的分片文件名
			parts := strings.Split(object.Key, "/")
			if len(parts) > 1 {
				uploadedParts = append(uploadedParts, parts[1])
			}
		}

		// 返回已上传的分片列表
		c.JSON(http.StatusOK, gin.H{
			"uploaded_parts": uploadedParts,
		})
	}
}

// 处理文件合并
func handleFileMerge(client *minio.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.PostForm("upload_id")
		fileName := c.PostForm("filename")

        // 设定合并后的文件名
        destFilePath := fmt.Sprintf("/tmp/%s/%s", uploadID, fileName) // 使用临时目录
		directory := path.Dir(destFilePath) 
        if err := os.MkdirAll(directory, 0755); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to create directory: " + err.Error()})
            return
        }

        destFile, err := os.Create(destFilePath)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to create file: " + err.Error()})
            return
        }
		defer destFile.Close()

		// 通过 ListObjects 获取所有的分片
		objectCh := client.ListObjects(context.Background(), bucketName, minio.ListObjectsOptions{
			Prefix:    fmt.Sprintf("%s/%s_part_", uploadID, fileName),
			Recursive: true,
		})

		// 收集所有分片
		var parts []string
		for object := range objectCh {
			if object.Err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to list objects: " + object.Err.Error()})
				return
			}
			parts = append(parts, object.Key)
		}

		// 对分片进行排序
		sort.Slice(parts, func(i, j int) bool {
			partNumI := strings.Split(parts[i], "_part_")[1]
			partNumJ := strings.Split(parts[j], "_part_")[1]
			numI, _ := strconv.Atoi(partNumI)
			numJ, _ := strconv.Atoi(partNumJ)
			return numI < numJ
		})

		// 按顺序合并文件
		for _, partKey := range parts {
			if err := downloadPartAndAppendToFile(client, bucketName, partKey, destFile); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to append part: " + err.Error()})
				return
			}
		}

        // 关闭文件以确保写入完成
        destFile.Close()

        // 上传合并后的文件到MinIO
        file, err := os.Open(destFilePath)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to open merged file: " + err.Error()})
            return
        }
        defer file.Close()

        // 设置上传的对象名称，通常与原始文件名相同或添加前缀
        objectName := fmt.Sprintf("%s_merged/%s", uploadID, fileName)
        _, err = client.PutObject(context.Background(), bucketName, objectName, file, -1, minio.PutObjectOptions{ContentType: "application/octet-stream"})
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload merged file: " + err.Error()})
            return
        }

		c.JSON(http.StatusOK, gin.H{"message": "File merged and uploaded successfully"})
	}
}

// 下载分片并追加到文件
func downloadPartAndAppendToFile(client *minio.Client, bucketName, objectPath string, destFile *os.File) error {
	reader, err := client.GetObject(context.Background(), bucketName, objectPath, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = io.Copy(destFile, reader)
	return err
}
