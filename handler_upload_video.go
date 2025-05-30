package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

	const maxMemory = 1 << 30 // 1 gigabyte
	http.MaxBytesReader(w, r.Body, maxMemory)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// Authenticate
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't retrieve video metadata from database.", err)
		return
	}

	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User does not own rights to this video.", nil)
		return
	}


	//TODO: do i need this parse step for video? Copied over from thumbnail.
	// Parse multipart form data
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse multipart form", err)
		return
	}

	// Save parsed data as multipart.File, multipart.FileHeader
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse file", err)
		return
	}
	
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")

	typeCheck, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusUnsupportedMediaType, "Error checking file type.", err)
	}
	if typeCheck != "video/mp4" {
		respondWithError(w, http.StatusUnsupportedMediaType, "File type not supported - please use MP4", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")

	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "temp file write malfunction.", err)
	}

	_, err = tempFile.Seek(0, 0)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "file pointer failed to reset.", err)
	}

	bucket := os.Getenv("S3_BUCKET")

	s3KeyBase := make([]byte, 32)
	rand.Read(s3KeyBase)

	s3KeyEncoded := base64.RawURLEncoding.EncodeToString(s3KeyBase)

	//TODO: refactor mp4 to a string literal if you end up supporting more video types.
	s3KeyFull := fmt.Sprintf("%s.mp4", s3KeyEncoded)


	//TODO: finish step 9
	cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket: &bucket,
		Key: &s3KeyFull,
		Body: tempFile,
		ContentType: &mediaType,
	})

}

