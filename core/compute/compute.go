package compute

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/rancher/agent/core/progress"
	"github.com/rancher/agent/core/storage"
	"github.com/rancher/agent/model"
	"github.com/rancher/agent/utilities/constants"
	dutils "github.com/rancher/agent/utilities/docker"
	"github.com/rancher/agent/utilities/utils"
	"golang.org/x/net/context"
	"reflect"
	"strconv"
	"github.com/docker/docker/api/types/filters"
)

type GpuSupport struct {
	gpuReservation []float64
	gpuFlag bool
}

var gpuSupport GpuSupport

func InitGPUReservation() {
	gpuSupport = GpuSupport{ gpuFlag:false }
}

func initHostGPU(host model.Host) error {
	if v, ok1 := host.Data["fields"]; ok1 {
		if vv, ok2 := v.(map[string]interface{})["createLabels"]; ok2 {
			if vvv, ok3 := vv.(map[string]interface{})["gpuReservation"]; ok3 {
				if gpu, err := strconv.ParseInt(vvv.(string), 10, 64); err == nil {
					if !gpuSupport.gpuFlag {
						gpuSupport.gpuReservation = make([]float64, gpu)
						for i:= 0; i < len(gpuSupport.gpuReservation); i++ {
							gpuSupport.gpuReservation[i] = 1.0
						}
						logrus.Infoln("TTTTTTTTTTTTTTTTTTAAAAAAAAA", reflect.TypeOf(host.Data["fields"]), host.Data["fields"].(map[string]interface{})["createLabels"].(map[string]interface{})["gpuReservation"], gpuSupport.gpuReservation)
					}
					gpuSupport.gpuFlag = true
				}
			}
		}
	}

	return nil
}

func preDispatchGpu(config *container.Config, instance model.Instance, host model.Host, dockerClient *client.Client) (gpuDispatched []int, gpuNeed int64, gpuRatio float64) {
	gpuRatio = 1.0

	// calculate gpu resource needed
	if gpuStr, ok := instance.Data.Fields.Labels["gpu"]; ok {
		logrus.Infoln("IIITTTTTTTTTTTTTTTT", instance.Data.Fields.Labels["gpu"])
		if gpu, err := strconv.ParseInt(gpuStr, 10, 64); err == nil {
			gpuNeed = gpu
		}
	}
	if ratioStr, ok := instance.Data.Fields.Labels["ratio"]; ok {
		logrus.Infoln("RRRTTTTTTTTTTTTTTTT", instance.Data.Fields.Labels["ratio"])
		if ratio, err := strconv.ParseFloat(ratioStr, 64); err == nil {
			gpuRatio = ratio
		}
	}

	if gpuNeed != 0 {
		gpuDispatched = make([]int, gpuNeed)
		temp := gpuRatio
		for i := 0; i < int(gpuNeed); i++ {
			for j := 0; j < len(gpuSupport.gpuReservation); j++ {
				if temp <= gpuSupport.gpuReservation[j] {
					gpuDispatched[i] = j
					gpuSupport.gpuReservation[j] -= temp
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
		logrus.Infoln("GGGTTTTTTTTTTT", gpuSupport.gpuReservation, "$$$", tempStr)

		// restore gpu resource in case it fail
		for i := 0; i < len(gpuDispatched); i++ {
			gpuSupport.gpuReservation[gpuDispatched[i]] += temp
		}
	}

	return gpuDispatched, gpuNeed, gpuRatio
}

func setGpuDeviceAndVolume(gpuDispatch []int, fields *model.InstanceFields, instance *model.Instance, client *client.Client) {
	if gpuDispatch != nil {
		fields.Devices = append(fields.Devices, "/dev/nvidiactl:/dev/nvidiactl:rwm", "/dev/nvidia-uvm:/dev/nvidia-uvm:rwm")
		for i := 0; i < len(gpuDispatch); i++ {
			tempStr := fmt.Sprintf("/dev/nvidia%v:/dev/nvidia%v:rwm", gpuDispatch[i], gpuDispatch[i])
			fields.Devices = append(fields.Devices, tempStr)
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

func postDispatchGpu(gpuDispatched []int, gpuRatio float64) {
	for i := 0; i < len(gpuDispatched); i++ {
		gpuSupport.gpuReservation[gpuDispatched[i]] -= gpuRatio
	}
}

func DoInstanceActivate(instance model.Instance, host model.Host, progress *progress.Progress, dockerClient *client.Client, infoData model.InfoData) error {

	if utils.IsNoOp(instance.ProcessData) {
		return nil
	}
	imageTag, err := getImageTag(instance)
	if err != nil {
		return errors.Wrap(err, constants.DoInstanceActivateError+"failed to get image tag")
	}

	instanceName := instance.Name
	parts := strings.Split(instance.UUID, "-")
	if len(parts) == 0 {
		return errors.Wrap(err, constants.DoInstanceActivateError+"Failed to parse UUID")
	}
	name := fmt.Sprintf("r-%s", instance.UUID)
	if str := constants.NameRegexCompiler.FindString(instanceName); str != "" {
		// container name is valid
		name = fmt.Sprintf("r-%s-%s", instanceName, parts[0])
	}

	if err = initHostGPU(host); err != nil {
		logrus.Infoln("..................... - initGpu fail", err)
	}

	config := container.Config{
		OpenStdin: true,
	}
	hostConfig := container.HostConfig{
		PublishAllPorts: false,
		Privileged:      instance.Data.Fields.Privileged,
		ReadonlyRootfs:  instance.Data.Fields.ReadOnly,
	}
	networkConfig := network.NetworkingConfig{}

	initializeMaps(&config, &hostConfig)

	utils.AddLabel(&config, constants.UUIDLabel, instance.UUID)

	if len(instanceName) > 0 {
		utils.AddLabel(&config, constants.ContainerNameLabel, instanceName)
	}

	gpuDispatched, gpuNeed, gpuRatio := preDispatchGpu(&config, instance, host, dockerClient)

	setGpuDeviceAndVolume(gpuDispatched, &instance.Data.Fields, &instance, dockerClient)

	setupFieldsHostConfig(instance.Data.Fields, &hostConfig)

	setupFieldsConfig(instance.Data.Fields, &config)

	setupPublishPorts(&hostConfig, instance)

	if err := setupDNSSearch(&hostConfig, instance); err != nil {
		return errors.Wrap(err, constants.DoInstanceActivateError+"failed to set up DNS search")
	}

	setupLinks(&hostConfig, instance)

	setupHostname(&config, instance)

	setupPorts(&config, instance, &hostConfig)

	if err := setupVolumes(&config, instance, &hostConfig, dockerClient, progress); err != nil {
		return errors.Wrap(err, constants.DoInstanceActivateError+"failed to set up volumes")
	}

	if err := setupNetworking(instance, host, &config, &hostConfig, dockerClient, infoData); err != nil {
		return errors.Wrap(err, constants.DoInstanceActivateError+"failed to set up networking")
	}

	setupProxy(instance, &config, getHostEntries())

	setupCattleConfigURL(instance, &config)

	setupNetworkingConfig(&networkConfig, instance)

	setupDeviceOptions(&hostConfig, instance, infoData)

	setupComputeResourceFields(&hostConfig, instance)

	setupHeathConfig(instance.Data.Fields, &config)

	setupLabels(instance.Data.Fields.Labels, &config)

	container, err := utils.GetContainer(dockerClient, instance, false)
	if err != nil {
		if !utils.IsContainerNotFoundError(err) {
			return errors.Wrap(err, constants.DoInstanceActivateError+"failed to get container")
		}
	}
	containerID := container.ID
	created := false
	if containerID == "" {
		newID, err := createContainer(dockerClient, &config, &hostConfig, &networkConfig, imageTag, instance, name, progress)
		if err != nil {
			return errors.Wrap(err, constants.DoInstanceActivateError+"failed to create container")
		}
		containerID = newID
		created = true
	}

	startErr := dutils.Serialize(func() error {
		return dockerClient.ContainerStart(context.Background(), containerID, types.ContainerStartOptions{})
	})
	if startErr != nil {
		if created {
			if err := utils.RemoveContainer(dockerClient, containerID); err != nil {
				return errors.Wrap(err, constants.DoInstanceActivateError+"failed to remove container")
			}
		}
		return errors.Wrap(startErr, constants.DoInstanceActivateError+"failed to start container")
	}

	// if nothing went wrong, dispatch gpu resource
	if gpuNeed != 0 {
		postDispatchGpu(gpuDispatched, gpuRatio)
	}

	logrus.Infof("rancher id [%v]: Container with docker id [%v] has been started", instance.ID, containerID)

	return nil
}

func DoInstancePull(params model.ImageParams, progress *progress.Progress, dockerClient *client.Client) (types.ImageInspect, error) {
	imageName := utils.ParseRepoTag(params.ImageUUID)
	existing, _, err := dockerClient.ImageInspectWithRaw(context.Background(), imageName)
	if err != nil && !client.IsErrImageNotFound(err) {
		return types.ImageInspect{}, errors.Wrap(err, constants.DoInstancePullError+"failed to inspect image")
	}
	if params.Mode == "cached" {
		return existing, nil
	}
	if params.Complete {
		_, err := dockerClient.ImageRemove(context.Background(), fmt.Sprintf("%s%s", imageName, params.Tag), types.ImageRemoveOptions{Force: true})
		if err != nil && !client.IsErrImageNotFound(err) {
			return types.ImageInspect{}, errors.Wrap(err, constants.DoInstancePullError+"failed to remove image")
		}
		return types.ImageInspect{}, nil
	}
	if err := storage.PullImage(params.Image, progress, dockerClient, params.ImageUUID); err != nil {
		return types.ImageInspect{}, errors.Wrap(err, constants.DoInstancePullError+"failed to pull image")
	}

	if len(params.Tag) > 0 {
		repoTag := fmt.Sprintf("%s%s", imageName, params.Tag)
		if err := dockerClient.ImageTag(context.Background(), imageName, repoTag); err != nil && !client.IsErrImageNotFound(err) {
			return types.ImageInspect{}, errors.Wrap(err, constants.DoInstancePullError+"failed to tag image")
		}
	}
	inspect, _, err2 := dockerClient.ImageInspectWithRaw(context.Background(), imageName)
	if err2 != nil && !client.IsErrImageNotFound(err) {
		return types.ImageInspect{}, errors.Wrap(err, constants.DoInstancePullError+"failed to inspect image")
	}
	return inspect, nil
}

func DoInstanceDeactivate(instance model.Instance, client *client.Client, timeout int) error {
	if utils.IsNoOp(instance.ProcessData) {
		return nil
	}
	t := time.Duration(timeout) * time.Second
	container, err := utils.GetContainer(client, instance, false)
	if err != nil {
		return errors.Wrap(err, constants.DoInstanceDeactivateError+"failed to get container")
	}
	client.ContainerStop(context.Background(), container.ID, &t)
	container, err = utils.GetContainer(client, instance, false)
	if err != nil {
		return errors.Wrap(err, constants.DoInstanceDeactivateError+"failed to get container")
	}
	if ok, err := isStopped(client, container); err != nil {
		return errors.Wrap(err, constants.DoInstanceDeactivateError+"failed to check whether container is stopped")
	} else if !ok {
		if killErr := client.ContainerKill(context.Background(), container.ID, "KILL"); killErr != nil {
			return errors.Wrap(killErr, constants.DoInstanceDeactivateError+"failed to kill container")
		}
	}
	if ok, err := isStopped(client, container); err != nil {
		return errors.Wrap(err, constants.DoInstanceDeactivateError+"failed to check whether container is stopped")
	} else if !ok {
		return fmt.Errorf("Failed to stop container %v", instance.UUID)
	}

	// release gpu
	if tempStr, ok := container.Labels["gpu_card"]; ok {
		logrus.Infoln("CCCTTTTTTTTTTTTTTTT", gpuSupport.gpuReservation, tempStr)
		tempSlice := strings.Split(tempStr, ",")
		ratioStr, ok := container.Labels["ratio"]
		var ratio float64
		if !ok {
			ratio = 1.0
		} else {
			ratio, _ = strconv.ParseFloat(ratioStr, 64)
		}
		for i := 0; i < len(tempSlice); i++ {
			if temp, err := strconv.ParseInt(tempSlice[i], 10, 64); err == nil {
				gpuSupport.gpuReservation[temp] += ratio
			}
		}
	}

	logrus.Infof("rancher id [%v]: Container with docker id [%v] has been deactivated", instance.ID, container.ID)
	return nil
}

func DoInstanceForceStop(request model.InstanceForceStop, dockerClient *client.Client) error {
	time := time.Duration(10)
	if stopErr := dockerClient.ContainerStop(context.Background(), request.ID, &time); client.IsErrContainerNotFound(stopErr) {
		logrus.Infof("container id %v not found", request.ID)
		return nil
	} else if stopErr != nil {
		return errors.Wrap(stopErr, constants.DoInstanceForceStopError+"failed to stop container")
	}
	return nil
}

func DoInstanceInspect(inspect model.InstanceInspect, dockerClient *client.Client) (types.ContainerJSON, error) {
	containerID := inspect.ID
	if containerID != "" {
		// inspect by id
		containerInspect, err := dockerClient.ContainerInspect(context.Background(), containerID)
		if err != nil && !client.IsErrContainerNotFound(err) {
			return types.ContainerJSON{}, errors.Wrap(err, constants.DoInstanceInspectError+"Failed to inspect container")
		} else if err == nil {
			return containerInspect, nil
		}
	}
	if inspect.Name != "" {
		// inspect by name
		containerList, err := dockerClient.ContainerList(context.Background(), types.ContainerListOptions{All: true})
		if err != nil {
			return types.ContainerJSON{}, errors.Wrap(err, constants.DoInstanceInspectError+"failed to list containers")
		}
		find := false
		result := types.Container{}
		name := fmt.Sprintf("/%s", inspect.Name)
		if resultWithNameInspect, ok := utils.FindFirst(containerList, func(c types.Container) bool {
			return utils.NameFilter(name, c)
		}); ok {
			result = resultWithNameInspect
			find = true
		}

		if find {
			inspectResp, err := dockerClient.ContainerInspect(context.Background(), result.ID)
			if err != nil && !client.IsErrContainerNotFound(err) {
				return types.ContainerJSON{}, errors.Wrap(err, constants.DoInstanceInspectError+"failed to inspect container")
			}
			return inspectResp, nil
		}
	}
	return types.ContainerJSON{}, errors.Errorf("container with id [%v] not found", containerID)
}

func DoInstanceRemove(instance model.Instance, dockerClient *client.Client) error {
	container, err := utils.GetContainer(dockerClient, instance, false)
	if err != nil {
		if utils.IsContainerNotFoundError(err) {
			return nil
		}
		return errors.Wrap(err, constants.DoInstanceRemoveError+"failed to get container")
	}
	if err := utils.RemoveContainer(dockerClient, container.ID); err != nil {
		return errors.Wrap(err, constants.DoInstanceRemoveError+"failed to remove container")
	}
	logrus.Infof("rancher id [%v]: Container with docker id [%v] has been removed", instance.ID, container.ID)
	return nil
}
