package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// Parse multipart form data
	const maxMemory = 10 << 20 // 10 megabytes
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse multipart form", err)
		return
	}

	// Save parsed data as multipart.File, multipart.FileHeader
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")

//TODO: add other supported image types
	fileExt := ""
	switch mediaType {
		case "image/jpeg":
			fileExt = "jpg"
		case "image/png":
			fileExt = "png"
		case "image/gif":
			fileExt = "gif"
		default:
			respondWithError(w, http.StatusBadRequest, "Invalid image type", fmt.Errorf("unsupported media type: %s", mediaType))
			return
	}

	localFilePath := fmt.Sprintf("%s.%s", videoID, fileExt)
	rootFilePath := filepath.Join(cfg.assetsRoot, localFilePath)

	// Create empty file for thumbnail
	tnFile, err := os.Create(rootFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't save file", err)
		return
	}

	// Use io to fill newly created file with image data.
	_, err = io.Copy(tnFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving image to file.", err)
	}

	// Retrieve video to be updated
	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}
	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to retreive this video.", err)
	}

	// Store file path for image file location
	dataURL := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, videoID, fileExt)
	
	videoData.ThumbnailURL = &dataURL

	// Update database with image file path
	if err := cfg.db.UpdateVideo(videoData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video metadata.", err)
	}
	
	updatedVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to retrieve this video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updatedVideo)
}
