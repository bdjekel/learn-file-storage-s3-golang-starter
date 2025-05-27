package main

import (
	"fmt"
	"io"
	"net/http"

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

	const maxMemory = 10 << 20 // 10 megabytes
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse multipart form", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse image data", err)
		return
	}
	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to retreive this video.", err)
	}

	newTN := thumbnail{
		data: imageData,
		mediaType: mediaType,
	}

	videoThumbnails[videoMetadata.ID] = newTN

	tnURL := fmt.Sprintf("http://localhost:%s/api/thumbnails/%v", cfg.port, videoMetadata.ID)
	videoMetadata.ThumbnailURL = &tnURL

	if err := cfg.db.UpdateVideo(videoMetadata); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video metadata.", err)
	}
	
	updatedVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to retrieve this video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updatedVideo)
}
