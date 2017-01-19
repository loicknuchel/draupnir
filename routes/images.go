package routes

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gocardless/draupnir/exec"
	"github.com/gocardless/draupnir/models"
	"github.com/gocardless/draupnir/store"
	"github.com/gorilla/mux"
)

type Images struct {
	Store    store.ImageStore
	Executor exec.Executor
}

func (i Images) List(w http.ResponseWriter, r *http.Request) {
	images, err := i.Store.List()
	if err != nil {
		http.Error(w, "routes error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	err = json.NewEncoder(w).Encode(images)
	if err != nil {
		http.Error(w, "json encoding failed", http.StatusInternalServerError)
		return
	}
}

type createImageRequest struct {
	BackedUpAt time.Time `json:"backed_up_at"`
}

func (i Images) Create(w http.ResponseWriter, r *http.Request) {
	var req createImageRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, fmt.Sprintf("json decoding failed: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	image := models.NewImage(req.BackedUpAt)
	image, err = i.Store.Create(image)
	if err != nil {
		http.Error(w, fmt.Sprintf("error creating image: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	err = i.Executor.CreateBtrfsSubvolume(image.ID)
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("error creating btrfs subvolume: %s", err.Error()), http.StatusInternalServerError,
		)
		return
	}

	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(image)
	if err != nil {
		http.Error(w, "json encoding failed", http.StatusInternalServerError)
		return
	}
}

func (i Images) Done(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	image, err := i.Store.Get(id)
	if err != nil {
		http.Error(w, "cannot find image", http.StatusNotFound)
		return
	}

	if image.Ready {
		http.Error(w, "image is already finalised", http.StatusBadRequest)
		return
	}

	err = i.Executor.FinaliseImage(image.ID)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "could not finalise image", http.StatusInternalServerError)
		return
	}

	image, err = i.Store.MarkAsReady(image)
	if err != nil {
		log.Print(err.Error())
		http.Error(w, "failed to mark image as ready", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(image)
	if err != nil {
		http.Error(w, "json encoding failed", http.StatusInternalServerError)
		return
	}
}
