package mikrotik

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"gopkg.in/routeros.v2"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TListOfStrings []string

type TParams []*TParam
type TParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Note  string `json:"note"`
}

type THosts []*THost
type THost struct {
	Name             string         `json:"name"`
	IP               string         `json:"ip"`
	Login            string         `json:"login"`
	Pass             string         `json:"pass"`
	PortAPI          int            `json:"port_api"`
	PortSSH          int            `json:"port_ssh"`
	BackupFolder     string         `json:"backup_folder"`
	TaskName         string         `json:"task_name"`
	UsersAliases     TListOfStrings `json:"users_aliases"`
	SchedulesAliases TListOfStrings `json:"schedules_aliases"`
	UsersAllowed     TListOfStrings `json:"users_allowed"`
	LastSeen         int64
	Task             *TTask
	Users            TUsers
	Groups           TGroups
	Schedules        TSchedules
	connections      tConnections
}

type tConnections struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	api  *routeros.Client
}

type TUsers []*TUser
type TUser struct {
	Login   string `json:"login"`
	Pass    string `json:"pass"`
	Group   string `json:"group"`
	Address string `json:"address"`
	Comment string `json:"comment"`
	Alias   string `json:"alias"`
	Key     string `json:"key"`
}

type TSchedules []*TSchedule
type TSchedule struct {
	Name      string `json:"name"`
	Disabled  string `json:"disabled"`
	StartDate string `json:"start-date"`
	StartTime string `json:"start-time"`
	Interval  string `json:"interval"`
	Policy    string `json:"policy"`
	Comment   string `json:"comment"`
	Script    string `json:"script"`
	Alias     string `json:"alias"`
	OnEvent   string
}

type TTasks []*TTask
type TTask struct {
	Name    string `json:"name"`
	Start   int64  `json:"start"`
	Delay   int64  `json:"delay"`
	Expired int64  `json:"expired"`
	Alert   int64  `json:"alert"`
	Note    string `json:"note"`
}

type TGroups []*TGroup
type TGroup struct {
	Name    string `json:"name"`
	Skin    string `json:"skin"`
	Comment string `json:"comment"`
	Policy  string `json:"policy"`
}

var Params TParams
var Hosts THosts
var Tasks TTasks
var Users TUsers
var Groups TGroups
var Schedules TSchedules
var cfgMain = "configs/main.json"

func init() {
	err := LoadConfig()
	if err != nil {
		log.Panic(err)
	}
}

func LoadConfig() error {
	err := LoadJSON(&Params, cfgMain)
	if err != nil {
		return err
	}
	dirCfg, err := Params.GetByName("dir_mikrotik-config")
	err = LoadJSON(&Hosts, dirCfg.Value+"hosts.json")
	if err != nil {
		return err
	}
	err = LoadJSON(&Tasks, dirCfg.Value+"tasks.json")
	if err != nil {
		return err
	}
	err = LoadJSON(&Users, dirCfg.Value+"users.json")
	if err != nil {
		return err
	}
	err = LoadJSON(&Groups, dirCfg.Value+"groups.json")
	if err != nil {
		return err
	}
	err = LoadJSON(&Schedules, dirCfg.Value+"schedules.json")
	if err != nil {
		return err
	}
	err = Schedules.LoadOnEventScripts()
	if err != nil {
		return err
	}
	for _, host := range Hosts {
		host.Users = Users.FilterByAliases(host.UsersAliases)
		host.Schedules = Schedules.FilterByAliases(host.SchedulesAliases)
		host.Groups = Groups
		host.Task, err = Tasks.GetByName(host.TaskName)
		if err != nil {
			return err
		}
	}
	return nil
}

func LoadJSON(variable interface{}, jsonPath string) error {
	var err error
	var jsonFile *os.File
	var jsonByte []byte
	jsonFile, err = os.Open(jsonPath)
	if err != nil {
		return err
	}
	jsonByte, err = ioutil.ReadAll(jsonFile)
	if err != nil {
		return err
	}
	err = json.Unmarshal(jsonByte, &variable)
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[INIT] Config \"%s\" loaded...", jsonFile.Name()))
	err = jsonFile.Close()
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) Run() {
	var delay int64
	err := host.StartManager()
	if err != nil {
		log.Println(fmt.Sprintf("[%s] manager error: \"%s\"", host.IP, err))
		delay = host.Task.Expired
	} else {
		delay = host.GetNextTime() - time.Now().Unix()
	}
	time.Sleep(time.Duration(delay) * time.Second)
	host.Run()
	return
}

func (host *THost) StartManager() error {
	var dir, err = Params.GetByName("dir_backup")
	if err != nil {
		return err
	}
	dir.Value = strings.Replace(dir.Value, "{host.name}", host.Name, -1)
	dir.Value = strings.Replace(dir.Value, "{host.ip}", host.IP, -1)

	log.Println(fmt.Sprintf("[%s] sequence for cleaning users", host.IP))
	err = host.CleanUsers()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for cleaning groups", host.IP))
	err = host.CleanGroups()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for cleaning schedules", host.IP))
	err = host.CleanSchedules()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for adding groups", host.IP))
	err = host.AddGroups()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for adding users", host.IP))
	err = host.AddUsers()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for adding backup folder", host.IP))
	err = host.MakeBackupFolder()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for adding schedules", host.IP))
	err = host.AddSchedules()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] sequence for backup directory", host.IP))
	err = host.DownloadFolder(host.BackupFolder, dir.Value, true)
	if err != nil {
		return err
	}
	host.Disconnect()
	return nil
}

func (host *THost) GetNextTime() int64 {
	var now = time.Now().Unix()
	var start = host.Task.Start
	var delay = host.Task.Delay
	next := (int64((now-start)/delay)+1)*delay + start
	return next
}

func (host *THost) CleanUsers() error {
	var err error
	usersPass := host.GetUsersAllowed()
	users, err := host.GetUsers()
	if err != nil {
		return err
	}
	for _, user := range users {
		if !usersPass.IsContain(user.Login) {
			err = host.RemoveUser(user.Login)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (host *THost) CleanGroups() error {
	var err error
	groups, err := host.GetGroups()
	if err != nil {
		return err
	}
	for _, group := range groups {
		if !host.Groups.IsContain(group.Name) {
			err = host.RemoveGroup(group.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (host *THost) CleanSchedules() error {
	var err error
	schedules, err := host.GetSchedules()
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if !host.Schedules.IsContain(schedule.Name) {
			err = host.RemoveSchedule(schedule.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (host *THost) AddUsers() error {
	for _, user := range host.Users {
		if host.IsContainUser(*user) {
			log.Println(fmt.Sprintf("[%s] host already contain user \"%s\"", host.IP, user.Login))
			continue
		}
		err := host.MakeUser(*user)
		if err != nil {
			log.Println(fmt.Sprintf("[%s] error: \"%s\"", host.IP, err))
			continue
		}
		if user.Key != "" {
			err = host.UploadKey(user.Key)
			if err != nil {
				return err
			}
			err = host.ImportSshKey(*user, 5000, 10)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (host *THost) AddGroups() error {
	for _, group := range host.Groups {
		if host.IsContainGroup(*group) {
			log.Println(fmt.Sprintf("[%s] host already contain group \"%s\"", host.IP, group.Name))
			continue
		}
		err := host.MakeGroup(*group)
		if err != nil {
			return err
		}
	}
	return nil
}

func (host *THost) AddSchedules() error {
	for _, schedule := range host.Schedules {
		if host.IsContainSchedule(schedule) {
			log.Println(fmt.Sprintf("[%s] host already contain schedule \"%s\"", host.IP, schedule.Name))
			continue
		}
		err := host.MakeSchedule(schedule)
		if err != nil {
			return err
		}
	}
	return nil
}

func (host *THost) ImportSshKey(user TUser, delay time.Duration, attempts int) error {
	log.Println(fmt.Sprintf("[%s] try import key \"%s\" for user \"%s\"", host.IP, user.Key, user.Login))
	for i := 1; i <= attempts; i++ {
		connApi, err := host.GetConnectionAPI()
		if err != nil {
			return err
		}
		time.Sleep(delay * time.Millisecond)
		_, err = connApi.Run("/user/ssh-keys/import", "=public-key-file="+user.Key, "=user="+user.Login)
		if err != nil {
			log.Println(fmt.Sprintf("[%s] [%s] error: \"%s\"", host.IP, user.Login, err.Error()))
			log.Println(fmt.Sprintf("[%s] %d try and %d milisecond later", host.IP, i, i*int(delay)))
			continue
		}
		log.Println(fmt.Sprintf("[%s] key \"%s\" imported for user \"%s\"", host.IP, user.Key, user.Login))
		return nil
	}
	err := errors.New("CmdImportSshKeys: all attempts used")
	return err
}

func (host *THost) MakeUser(user TUser) error {
	log.Println(fmt.Sprintf("[%s] adding user \"%s\"", host.IP, user.Login))
	if user.Pass == "" {
		user.GeneratePassword(512)
		log.Println(fmt.Sprintf("[%s] user \"%s\" password is empty and has been generated", host.IP, user.Login))
	}
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	_, err = connApi.Run("/user/add", "=name="+user.Login, "=password="+user.Pass, "=group="+user.Group, "=address="+user.Address, "=comment="+user.Comment, "=disabled=no")
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) MakeGroup(group TGroup) error {
	log.Println(fmt.Sprintf("[%s] adding group \"%s\"", host.IP, group.Name))
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	_, err = connApi.Run("/user/group/add", "=name="+group.Name, "=skin="+group.Skin, "=comment="+group.Comment, "=policy="+group.Policy)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) MakeSchedule(schedule *TSchedule) error {
	log.Println(fmt.Sprintf("[%s] adding schedule \"%s\"", host.IP, schedule.Name))
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	_, err = connApi.Run("/system/scheduler/add",
		"=name="+schedule.Name,
		"=disabled="+schedule.Disabled,
		"=start-date="+schedule.StartDate,
		"=start-time="+schedule.StartTime,
		"=interval="+schedule.Interval,
		"=policy="+schedule.Policy,
		"=comment="+schedule.Comment,
		"=on-event="+schedule.OnEvent,
	)
	if err != nil {
		log.Println(err)
		return err
	}
	return nil
}

func (host *THost) GetUsers() ([]*TUser, error) {
	var err error
	var users []*TUser
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return []*TUser{}, err
	}
	res, err := connApi.Run("/user/print")
	if err != nil {
		return []*TUser{}, err
	}
	for _, el := range res.Re {
		user := TUser{
			Login:   el.Map["name"],
			Comment: el.Map["comment"],
			Address: el.Map["address"],
			Group:   el.Map["group"],
		}
		users = append(users, &user)
	}
	return users, nil
}

func (host *THost) GetSchedules() ([]*TSchedule, error) {
	var err error
	var schedules []*TSchedule
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return []*TSchedule{}, err
	}
	res, err := connApi.Run("/system/scheduler/print")
	if err != nil {
		return []*TSchedule{}, err
	}
	for _, el := range res.Re {
		schedule := TSchedule{
			Name:      el.Map["name"],
			StartDate: el.Map["start-date"],
			StartTime: el.Map["start-time"],
			Interval:  el.Map["interval"],
			Policy:    el.Map["policy"],
			Comment:   el.Map["comment"],
			OnEvent:   el.Map["on-event"],
		}
		schedules = append(schedules, &schedule)
	}
	return schedules, nil
}

func (host *THost) GetGroups() (TGroups, error) {
	var groups TGroups
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return TGroups{}, err
	}
	res, err := connApi.Run("/user/group/print")
	if err != nil {
		return TGroups{}, err
	}
	for _, el := range res.Re {
		group := TGroup{Name: el.Map["name"], Skin: el.Map["skin"], Comment: el.Map["comment"], Policy: el.Map["policy"]}
		groups = append(groups, &group)
	}
	return groups, nil
}

func (host *THost) UploadKey(key string) error {
	log.Println(fmt.Sprintf("[%s] uploading key \"%s\"", host.IP, key))
	connSftp, err := host.GetConnectionSFTP()
	if err != nil {
		return err
	}
	fileDst, err := connSftp.Create(key)
	if err != nil {
		return err
	}
	defer func() { _ = fileDst.Close }()
	dirKeys, err := Params.GetByName("dir_ssh-pub-keys")
	if err != nil {
		return err
	}
	fileSrc, err := os.Open(dirKeys.Value + key)
	if err != nil {
		return err
	}
	defer func() { _ = fileSrc.Close }()
	_, err = io.Copy(fileDst, fileSrc)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) RemoveUser(user string) error {
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] delete user \"%s\"", host.IP, user))
	_, err = connApi.Run("/user/remove", "=numbers="+user)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) RemoveGroup(group string) error {
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] delete group \"%s\"", host.IP, group))
	_, err = connApi.Run("/user/group/remove", "=numbers="+group)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) RemoveSchedule(schedule string) error {
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return err
	}
	log.Println(fmt.Sprintf("[%s] delete schedule \"%s\"", host.IP, schedule))
	_, err = connApi.Run("/system/scheduler/remove", "=numbers="+schedule)
	if err != nil {
		return err
	}
	return nil
}

func (params *TParams) GetByName(name string) (TParam, error) {
	for _, param := range *params {
		if param.Name == name {
			return *param, nil
		}
	}
	err := errors.New("param does not exist")
	return TParam{}, err
}

func (users TUsers) FilterByAliases(aliases TListOfStrings) []*TUser {
	var slice []*TUser
	for _, user := range users {
		if aliases.IsContain(user.Alias) {
			slice = append(slice, user)
		}
	}
	return slice
}

func (tasks TTasks) GetByName(name string) (*TTask, error) {
	for _, task := range tasks {
		if task.Name == name {
			return task, nil
		}
	}
	err := errors.New("task does not exist")
	return &TTask{}, err
}

func (schedules *TSchedules) FilterByAliases(aliases TListOfStrings) []*TSchedule {
	var slice []*TSchedule
	for _, schedule := range *schedules {
		if aliases.IsContain(schedule.Alias) {
			slice = append(slice, schedule)
		}
	}
	return slice
}

func (schedules *TSchedules) LoadOnEventScripts() error {
	var dir, err = Params.GetByName("dir_scripts")
	if err != nil {
		return err
	}
	for _, schedule := range *schedules {
		byteContent, err := ioutil.ReadFile(dir.Value + schedule.Script)
		if err != nil {
			log.Println(fmt.Sprintf("[WARNING] script \"%s\" does not exist", schedule.Script))
			schedule.OnEvent = ""
		} else {
			content := string(byteContent)
			schedule.OnEvent = content
		}
	}
	return nil
}

func (host *THost) GetUsersAllowed() TListOfStrings {
	var usersPass TListOfStrings
	usersPass = append(usersPass, host.Login)
	usersPass = append(usersPass, host.UsersAllowed...)
	for _, user := range host.Users {
		usersPass = append(usersPass, user.Login)
	}
	return usersPass
}

func (host *THost) MakeDir(dir string) error {
	connSftp, err := host.GetConnectionSFTP()
	if err != nil {
		return err
	}
	err = connSftp.MkdirAll(dir)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) MakeBackupFolder() error {
	if host.BackupFolder != "" {
		err := host.MakeDir(host.BackupFolder)
		if err != nil {
			return err
		}
	}
	return nil
}

func (host *THost) MakeExport(path string) (string, error) {
	var err error
	dir := filepath.ToSlash(filepath.Dir(path))
	file := filepath.Base(path)
	path = dir + "/" + file
	err = host.MakeDir(dir)
	if err != nil {
		return "", err
	}
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return "", err
	}
	_, err = connApi.Run("/export", "=terse", "=file="+path)
	if err != nil {
		return "", err
	}
	return path + ".rsc", nil
}

func (host *THost) MakeBackup(path string) (string, error) {
	var err error
	dir := filepath.ToSlash(filepath.Dir(path))
	file := filepath.Base(path)
	path = dir + "/" + file
	err = host.MakeDir(dir)
	if err != nil {
		return "", err
	}
	connApi, err := host.GetConnectionAPI()
	if err != nil {
		return "", err
	}
	_, err = connApi.Run("/system/backup/save", "=name="+path)
	if err != nil {
		return "", err
	}
	return path + ".backup", nil
}

func (host *THost) RemoveFile(path string) error {
	var err error
	path = filepath.ToSlash(filepath.Dir(path)) + "/" + filepath.Base(path)
	connSftp, err := host.GetConnectionSFTP()
	if err != nil {
		return err
	}
	err = connSftp.Remove(path)
	if err != nil {
		return err
	}
	return nil
}

func (host *THost) IsContainUser(user TUser) bool {
	usersInside, err := host.GetUsers()
	if err != nil {
		return false
	}
	for _, userInside := range usersInside {
		if userInside.Login == user.Login {
			return true
		}
	}
	return false
}

func (host *THost) IsContainGroup(group TGroup) bool {
	groupsInside, err := host.GetGroups()
	if err != nil {
		return false
	}
	for _, groupInside := range groupsInside {
		if groupInside.Name == group.Name {
			return true
		}
	}
	return false
}

func (host *THost) IsContainSchedule(schedule *TSchedule) bool {
	schedulesInside, err := host.GetSchedules()
	if err != nil {
		return false
	}
	for _, scheduleInside := range schedulesInside {
		if scheduleInside.Name == schedule.Name {
			return true
		}
	}
	return false
}

func (host *THost) GetSshClientConfig() *ssh.ClientConfig {
	config := &ssh.ClientConfig{
		User:            host.Login,
		Auth:            []ssh.AuthMethod{ssh.Password(host.Pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	return config
}

func (host *THost) DownloadFolder(dirSrc string, dirDst string, delete bool) error {
	var err error
	connSftp, err := host.GetConnectionSFTP()
	if err != nil {
		return err
	}
	files, err := connSftp.ReadDir(dirSrc)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() {
			err = host.DownloadFolder(dirSrc+file.Name(), dirDst+file.Name(), delete)
			if err != nil {
				return err
			}
		} else {
			err := host.DownloadFile(dirSrc+"/"+file.Name(), dirDst, delete)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (host *THost) DownloadFile(pathSrc string, dirDst string, delete bool) error {
	var err error
	file := filepath.Base(pathSrc)
	pathSrc = filepath.ToSlash(filepath.Dir(pathSrc)) + "/" + file
	pathDst := dirDst + "/" + file
	connSftp, err := host.GetConnectionSFTP()
	if err != nil {
		return err
	}
	fileSrc, err := connSftp.Open(pathSrc)
	if err != nil {
		return err
	}
	err = os.MkdirAll(dirDst, 0755)
	if err != nil {
		return err
	}
	fileDst, err := os.Create(pathDst)
	if err != nil {
		return err
	}
	_, err = io.Copy(fileDst, fileSrc)
	if err != nil {
		return err
	}
	err = fileDst.Sync()
	if err != nil {
		return err
	}
	err = fileSrc.Close()
	if err != nil {
		return err
	}
	if delete {
		err = connSftp.Remove(pathSrc)
		if err != nil {
			return err
		}
	}
	err = fileDst.Close()
	if err != nil {
		return err
	}
	return nil
}

func (users TListOfStrings) IsContain(string string) bool {
	for _, element := range users {
		if element == string {
			return true
		}
	}
	return false
}

func (groups TGroups) IsContain(group string) bool {
	for _, element := range groups {
		if element.Name == group {
			return true
		}
	}
	return false
}

func (users TUsers) IsContain(user string) bool {
	for _, element := range users {
		if element.Login == user {
			return true
		}
	}
	return false
}

func (schedules *TSchedules) IsContain(name string) bool {
	for _, schedule := range *schedules {
		if schedule.Name == name {
			return true
		}
	}
	return false
}

func (host *THost) GetConnectionSSH() (*ssh.Client, error) {
	var err error
	if host.connections.ssh == nil {
		log.Println(fmt.Sprintf("[%s] connection via SSH", host.IP))
		host.connections.ssh, err = ssh.Dial("tcp", fmt.Sprintf("%s:%d", host.IP, host.PortSSH), host.GetSshClientConfig())
		if err != nil {
			return nil, err
		}
	}
	return host.connections.ssh, nil
}

func (host *THost) GetConnectionSFTP() (*sftp.Client, error) {
	var err error
	var connSSH *ssh.Client
	if host.connections.sftp == nil {
		connSSH, err = host.GetConnectionSSH()
		if err != nil {
			return nil, err
		}
		log.Println(fmt.Sprintf("[%s] connection via SFTP", host.IP))
		host.connections.sftp, err = sftp.NewClient(connSSH)
		if err != nil {
			return nil, err
		}
	}
	return host.connections.sftp, nil
}

func (host *THost) GetConnectionAPI() (*routeros.Client, error) {
	var err error
	if host.connections.api == nil {
		log.Println(fmt.Sprintf("[%s] connection via API", host.IP))
		host.connections.api, err = routeros.Dial(fmt.Sprintf("%s:%d", host.IP, host.PortAPI), host.Login, host.Pass)
		if err != nil {
			return nil, err
		}
	}
	return host.connections.api, nil
}

func (host *THost) Disconnect() {
	if host.connections.api != nil {
		log.Println(fmt.Sprintf("[%s] disconnection via API", host.IP))
		host.connections.api.Close()
		host.connections.api = nil
	}
	if host.connections.sftp != nil {
		log.Println(fmt.Sprintf("[%s] disconnection via SFTP", host.IP))
		_ = host.connections.sftp.Close()
		host.connections.sftp = nil
	}
	if host.connections.ssh != nil {
		log.Println(fmt.Sprintf("[%s] disconnection via SSH", host.IP))
		_ = host.connections.ssh.Close()
		host.connections.ssh = nil
	}
}

func (user *TUser) GeneratePassword(length int) {
	var lowerCharSet = "abcdedfghijklmnopqrst"
	var upperCharSet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var specialCharSet = "!@#$%&*"
	var numberSet = "0123456789"
	var allCharSet = lowerCharSet + upperCharSet + specialCharSet + numberSet
	var password strings.Builder
	minSpecialChar := 1
	minNum := 1
	minUpperCase := 1
	for i := 0; i < minSpecialChar; i++ {
		random := rand.Intn(len(specialCharSet))
		password.WriteString(string(specialCharSet[random]))
	}
	for i := 0; i < minNum; i++ {
		random := rand.Intn(len(numberSet))
		password.WriteString(string(numberSet[random]))
	}
	for i := 0; i < minUpperCase; i++ {
		random := rand.Intn(len(upperCharSet))
		password.WriteString(string(upperCharSet[random]))
	}
	remainingLength := length - minSpecialChar - minNum - minUpperCase
	for i := 0; i < remainingLength; i++ {
		random := rand.Intn(len(allCharSet))
		password.WriteString(string(allCharSet[random]))
	}
	inRune := []rune(password.String())
	rand.Shuffle(len(inRune), func(i, j int) {
		inRune[i], inRune[j] = inRune[j], inRune[i]
	})
	user.Pass = string(inRune)
}
