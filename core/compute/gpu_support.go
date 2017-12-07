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
	"sort"
)

// generate gpu detection which return the amount of the gpu cards
func gpuDetection(host model.Host) (gpu int) {
	if v, ok1 := host.Data["fields"]; ok1 {
		if vv, ok2 := v.(map[string]interface{})["labels"]; ok2 {
			if vvv, ok3 := vv.(map[string]interface{})["gpuReservation"]; ok3 {
				if gpuDetected, err := strconv.ParseInt(vvv.(string), 10, 64); err == nil {
					gpu = int(gpuDetected)
					logrus.Infoln("GPU Reservation detected: ", gpu)
				}
			}
		}
	}
	return
}

// get gpu resources in use
func getGpuAllocated(dockerClient *client.Client, gpu int) (gpuAllocated []float64) {
	gpuAllocated = make([]float64, gpu)

	if containers, err := dockerClient.ContainerList(context.Background(), types.ContainerListOptions{All: true}); err == nil {
		for _, con := range containers {
			if con.State != "running" {
				continue
			}

			if tempStr, ok := con.Labels["gpu_card"]; ok {
				tempSlice := strings.Split(tempStr, ",")
				ratioStr, ok := con.Labels["ratio"]
				var ratio float64
				if !ok {
					ratio = 1.0
				} else {
					ratio, _ = strconv.ParseFloat(ratioStr, 64)
				}
				for i := 0; i < len(tempSlice); i++ {
					if temp, err := strconv.ParseInt(tempSlice[i], 10, 64); err == nil {
						gpuAllocated[temp] += ratio
					}
				}
			}
		}
	}

	return
}

func getGpuNeeded(instance model.Instance) (gpuNeed int, gpuRatio float64, balanced bool) {
	gpuRatio = 1.0
	balanced = false

	// calculate gpu resource needed
	if gpuStr, ok := instance.Data.Fields.Labels["gpu"]; ok {
		if gpu, err := strconv.ParseInt(gpuStr, 10, 64); err == nil {
			gpuNeed = int(gpu)
		}
	}
	if ratioStr, ok := instance.Data.Fields.Labels["ratio"]; ok {
		if ratio, err := strconv.ParseFloat(ratioStr, 64); err == nil {
			gpuRatio = ratio
		}
	}
	if strategyStr, ok := instance.Data.Fields.Labels["strategy"]; ok {
		if strategyStr == "balanced" {
			balanced = true
		}
	}

	return
}

type Pair struct {
	gpuUsed float64
	index int
}

type Pairs []Pair

func (pairs Pairs) Len() int {
	return len(pairs)
}

func (pairs Pairs) Swap(i, j int)  {
	pairs[i], pairs[j] = pairs[j], pairs[i]
}

func (pairs Pairs) Less(i, j int) bool {
	return pairs[i].gpuUsed < pairs[j].gpuUsed
}

func dispatchGpu(gpuAllocated []float64, config *container.Config, gpuNeed int, gpuRatio float64, balanced bool) (gpuDispatched []int) {
	if gpuNeed != 0 {
		tempPairs := make(Pairs, len(gpuAllocated))
		for i := 0; i < len(tempPairs); i++ {
			tempPairs[i] = Pair{gpuAllocated[i], i}
		}
		if balanced {
			sort.Sort(tempPairs)
		} else {
			sort.Sort(sort.Reverse(tempPairs))
		}

		for _, pair := range tempPairs {
			if len(gpuDispatched) >= gpuNeed {
				break
			}

			if 10 - pair.gpuUsed >= gpuRatio {
				gpuDispatched = append(gpuDispatched, pair.index)
				gpuAllocated[pair.index] += gpuRatio
			}
		}

		// if not satisfied, recover gpuAllocated and return nil
		if len(gpuDispatched) != gpuNeed {
			for _, i := range gpuDispatched {
				gpuAllocated[i] -= gpuRatio
			}
			logrus.Errorln("GPU Distribution failed. Current Allocated: ", gpuAllocated)
			gpuDispatched = nil
			return nil
		}

		tempStr := ""
		for i := 0; i < int(gpuNeed) - 1; i++ {
			tempStr = tempStr + strconv.Itoa(gpuDispatched[i]) + ","
		}
		tempStr = tempStr + strconv.Itoa(gpuDispatched[gpuNeed - 1])
		utils.AddLabel(config, "gpu_card", tempStr)
		logrus.Infoln("GPU Resource: ", gpuAllocated, " , allocate: ", tempStr)
	}

	return gpuDispatched
}

func setGpuDeviceAndVolume(gpuDispatch []int, instance *model.Instance, client *client.Client) {
	if gpuDispatch != nil {
		instance.Data.Fields.Devices = append(instance.Data.Fields.Devices, "/dev/nvidiactl:/dev/nvidiactl:rwm", "/dev/nvidia-uvm:/dev/nvidia-uvm:rwm")
		for i := 0; i < len(gpuDispatch); i++ {
			tempStr := fmt.Sprintf("/dev/nvidia%v:/dev/nvidia%v:rwm", gpuDispatch[i], i)
			instance.Data.Fields.Devices = append(instance.Data.Fields.Devices, tempStr)
		}

		vols, err := client.VolumeList(context.Background(), filters.NewArgs())
		if err == nil {
			tempStr := ""
			for _, vol := range vols.Volumes {
				if vol.Driver == "nvidia-docker" {
					tempStr = fmt.Sprintf("%s:/usr/local/nvidia:ro", vol.Name)
				}
			}

			if tempStr != "" {
				instance.Data.Fields.DataVolumes = append(instance.Data.Fields.DataVolumes, tempStr)
			}
		} else {
			logrus.Infoln("Cant't find gpu volume, maybe nvidia-docker hasn't been installed. ", err)
		}
	}
}