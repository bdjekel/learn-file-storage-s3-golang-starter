package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

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
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error storing video file.", err)
		return
	}

	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "temp file write malfunction.", err)
		return
	}

	_, err = tempFile.Seek(0, 0)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "file pointer failed to reset.", err)
		return
	}
	
	// Process video for fast start
	updatedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video for fast start", err)
		return
	}
	
	processedVideo, err := os.Open(updatedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening processed video file.", err)
		return
	}

	defer os.Remove(processedVideo.Name())
	defer processedVideo.Close()
	
	// Create file name for uploaded video.
	bucket := os.Getenv("S3_BUCKET")

	// Cryptographically random 32-byte integer as base "id"
	s3KeyBase := make([]byte, 32)
	rand.Read(s3KeyBase)
	
	// Encode byte slice into string.
	s3KeyBaseEncoded := base64.RawURLEncoding.EncodeToString(s3KeyBase)
	
	// Get aspect ratio for file name prefix
	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error with function getVideoAspectRatio.", err)
		return
	}
	
	var ratioPrefix string
	
	switch aspectRatio {
	case "16:9":
		ratioPrefix = "portrait"
	case "9:16":
		ratioPrefix = "landscape"
	default:
		ratioPrefix = "other"		
	}
	
	
	// Combine prefix, encoded key base, and file extension
	
	//TODO: refactor "mp4" to a string literal if you end up supporting more video types.
	s3KeyFull := fmt.Sprintf("%s/%s.mp4", ratioPrefix, s3KeyBaseEncoded)
	

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket: &bucket,
		Key: &s3KeyFull,
		Body: processedVideo,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload video to S3", err)
		return
	}


	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, cfg.s3Region, s3KeyFull)

	videoData.VideoURL = &videoURL

	if err := cfg.db.UpdateVideo(videoData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating video in database.", err)
		return
	}

//TODO: maybe modify error message. It could be misleading here as this is not the auth step.
	updatedVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to retrieve this video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updatedVideo)
}


func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return "Error retrieving aspect ratio.", err
	}

	var jsonOut struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	err = json.Unmarshal(out.Bytes(), &jsonOut)
	if err != nil {
		return "Error retrieving aspect ratio.", err
	}

	if len(jsonOut.Streams) == 0 {
		return "Error retrieving aspect ratio.", fmt.Errorf("no video streams found")
	}

	if jsonOut.Streams[0].Width == 0 || jsonOut.Streams[0].Height == 0 {
		return "Error retrieving aspect ratio.", err
	}

	aspectRatio := float64(jsonOut.Streams[0].Width) / float64(jsonOut.Streams[0].Height)

	switch {
	case aspectRatio >= 1.7 && aspectRatio <= 1.8:  // around 16/9 ≈ 1.77
		fmt.Printf("ASPECT RATIO ==> %v", aspectRatio)
		return "9:16", nil
	case aspectRatio >= 0.55 && aspectRatio <= 0.57:  // around 9/16 ≈ 0.5625
		fmt.Printf("ASPECT RATIO ==> %v", aspectRatio)
		return "16:9", nil
	default:
		fmt.Printf("ASPECT RATIO ==> %v", aspectRatio)
		return "other", nil
	}

}

func processVideoForFastStart(filePath string) (string, error) {
	updatedFilePath := fmt.Sprintf("%s.processing", filePath)
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", updatedFilePath)

	err := cmd.Run()
	if err != nil {
		return "Error with processVideoForFastStart function.", err
	}

	return updatedFilePath, nil
}