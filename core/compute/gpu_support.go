package compute

import (
	"github.com/rancher/agent/model"
	"github.com/docker/docker/client"
	"strings"
	"strconv"
	"golang.org/x/net/context"
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/rancher/agent/utilities/utils"
	"fmt"
	"github.com/docker/docker/api/types/filters"
)

func generateGpuDetection() func(host model.Host, dockerClient *client.Client) int {
	flag := false
	gpu := 0

	return func(host model.Host, dockerClient *client.Client) int {
		if !flag {
			if v, ok1 := host.Data["fields"]; ok1 {
				if vv, ok2 := v.(map[string]interface{})["createLabels"]; ok2 {
					if vvv, ok3 := vv.(map[string]interface{})["gpuReservation"]; ok3 {
						if gpuDetected, err := strconv.ParseInt(vvv.(string), 10, 64); err == nil {
							gpu = int(gpuDetected)
						}
					}
				}
			}
			flag = true
		}
		return gpu
	}
}

func getGpuReservation(dockerClient *client.Client, gpu int) (gpuReservation []float64) {
	gpuReservation = make([]float64, gpu)
	for i:= 0; i < len(gpuReservation); i++ {
		gpuReservation[i] = 1.0
	}

	if containers, err := dockerClient.ContainerList(context.Background(), types.ContainerListOptions{All: true}); err == nil {
		for _, con := range containers {
			if tempStr, ok := con.Labels["gpu_card"]; ok {
				logrus.Infoln("EEEEEEEEEEEXXXXXXXXX", tempStr)

				tempSlice := strings.Split(tempStr, ",")
				ratioStr, ok := con.Labels["ratio"]
				var ratio float64
				if !ok {
					ratio = 1.0
				} else {
					ratio, _ = strconv.ParseFloat(ratioStr, 64)
				}
				for i := 0; i < len(tempSlice); i++ {
					if temp, err := strconv.ParseInt(tempSlice[i], 10, 64); err == nil && gpuReservation[temp] >= ratio {
						gpuReservation[temp] -= ratio
					}
				}
			}
		}
	}

	return
}

func getGpuNeeded(instance model.Instance) (gpuNeed int, gpuRatio float64) {
	gpuRatio = 1.0

	// calculate gpu resource needed
	if gpuStr, ok := instance.Data.Fields.Labels["gpu"]; ok {
		logrus.Infoln("IIITTTTTTTTTTTTTTTT", instance.Data.Fields.Labels["gpu"])
		if gpu, err := strconv.ParseInt(gpuStr, 10, 64); err == nil {
			gpuNeed = int(gpu)
		}
	}
	if ratioStr, ok := instance.Data.Fields.Labels["ratio"]; ok {
		logrus.Infoln("RRRTTTTTTTTTTTTTTTT", instance.Data.Fields.Labels["ratio"])
		if ratio, err := strconv.ParseFloat(ratioStr, 64); err == nil {
			gpuRatio = ratio
		}
	}

	return
}

func dispatchGpu(gpuReservation []float64, config *container.Config, gpuNeed int, gpuRatio float64) (gpuDispatched []int) {
	if gpuNeed != 0 {
		gpuDispatched = make([]int, gpuNeed)
		temp := gpuRatio
		for i := 0; i < int(gpuNeed); i++ {
			for j := 0; j < len(gpuReservation); j++ {
				if temp <= gpuReservation[j] {
					gpuDispatched[i] = j
					gpuReservation[j] -= temp
					logrus.Infoln("GGGTTTTTTTTTTT", gpuDispatched)
					break
				}
			}
		}
		tempStr := ""
		for i := 0; i < int(gpuNeed) - 1; i++ {
			tempStr = tempStr + strconv.Itoa(gpuDispatched[i]) + ","
		}
		tempStr = tempStr + strconv.Itoa(gpuDispatched[int(gpuNeed) - 1])
		utils.AddLabel(config, "gpu_card", tempStr)
		logrus.Infoln("GGGTTTTTTTTTTT", gpuReservation, "$$$", tempStr)
	}

	return gpuDispatched
}

func setGpuDeviceAndVolume(gpuDispatch []int, instance *model.Instance, client *client.Client) {
	if gpuDispatch != nil {
		instance.Data.Fields.Devices = append(instance.Data.Fields.Devices, "/dev/nvidiactl:/dev/nvidiactl:rwm", "/dev/nvidia-uvm:/dev/nvidia-uvm:rwm")
		for i := 0; i < len(gpuDispatch); i++ {
			tempStr := fmt.Sprintf("/dev/nvidia%v:/dev/nvidia%v:rwm", gpuDispatch[i], gpuDispatch[i])
			instance.Data.Fields.Devices = append(instance.Data.Fields.Devices, tempStr)
		}

		vols, err := client.VolumeList(context.Background(), filters.NewArgs())
		if err == nil {
			for _, vol := range vols.Volumes {
				if vol.Driver == "nvidia-docker" {
					tempStr := fmt.Sprintf("%s:/usr/local/nvidia:ro", vol.Name)
					instance.Data.Fields.DataVolumes = append(instance.Data.Fields.DataVolumes, tempStr)
					break
				}
			}
		} else {
			logrus.Infoln("CCCCCCCCCCCLLLLLLLLLLLLLL - docker", err)
		}
	}
}