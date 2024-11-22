package main

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strconv"

    "github.com/gin-gonic/gin"
)

func main() {
    router := gin.Default()

    router.POST("/upload", uploadHandler)
    router.POST("/merge", mergeHandler)
    router.POST("/check", checkHandler)
    router.GET("/", func(c *gin.Context) {
        c.File("./static/index.html")
    })
    router.Static("/static", "./static")

    fmt.Println("Server started at http://localhost:8080")
    router.Run(":8080")
}

func uploadHandler(c *gin.Context) {
    fileHash := c.PostForm("fileHash")
    chunkIndex := c.PostForm("chunkIndex")

    index, err := strconv.Atoi(chunkIndex)
    if err != nil {
        c.String(http.StatusBadRequest, "Invalid chunk index")
        return
    }

    file, err := c.FormFile("file")
    if err != nil {
        c.String(http.StatusBadRequest, "Failed to get file")
        return
    }

    // 创建存储分片的目录
    chunkDir := filepath.Join("chunks", fileHash)
    os.MkdirAll(chunkDir, os.ModePerm)

    // 保存分片
    chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.tmp", index))
    err = c.SaveUploadedFile(file, chunkPath)
    if err != nil {
        c.String(http.StatusInternalServerError, "Failed to save chunk")
        return
    }

    c.String(http.StatusOK, "Chunk uploaded successfully")
}


func mergeHandler(c *gin.Context) {
    fileHash := c.PostForm("fileHash")
    totalChunks := c.PostForm("totalChunks")
    filename := c.PostForm("filename")

    total, err := strconv.Atoi(totalChunks)
    if err != nil {
        c.String(http.StatusBadRequest, "Invalid total chunks")
        return
    }

    chunkDir := filepath.Join("chunks", fileHash)
    destPath := filepath.Join("uploads", filename)

    destFile, err := os.Create(destPath)
    if err != nil {
        c.String(http.StatusInternalServerError, "Failed to create destination file")
        return
    }
    defer destFile.Close()

    // 合并分片
    for i := 0; i < total; i++ {
        chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.tmp", i))
        chunkFile, err := os.Open(chunkPath)
        if err != nil {
            c.String(http.StatusInternalServerError, fmt.Sprintf("Failed to open chunk %d", i))
            return
        }

        _, err = io.Copy(destFile, chunkFile)
        chunkFile.Close()
        if err != nil {
            c.String(http.StatusInternalServerError, fmt.Sprintf("Failed to write chunk %d", i))
            return
        }
    }

    // 清理分片
    os.RemoveAll(chunkDir)

    c.String(http.StatusOK, "File merged successfully")
}


func checkHandler(c *gin.Context) {
    fileHash := c.PostForm("fileHash")
    totalChunks := c.PostForm("totalChunks")

    total, err := strconv.Atoi(totalChunks)
    if err != nil {
        c.String(http.StatusBadRequest, "Invalid total chunks")
        return
    }

    chunkDir := filepath.Join("chunks", fileHash)
    uploadedChunks := []int{}

    for i := 0; i < total; i++ {
        chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.tmp", i))
        if _, err := os.Stat(chunkPath); err == nil {
            // 分片已存在
            uploadedChunks = append(uploadedChunks, i)
        }
    }

    // 返回已上传的分片列表
    c.JSON(http.StatusOK, gin.H{
        "uploaded": uploadedChunks,
    })
}

