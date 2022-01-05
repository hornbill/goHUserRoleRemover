package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/cheggaaa/pb"
	apiLib "github.com/hornbill/goApiLib"
	hornbillHelpers "github.com/hornbill/goHornbillHelpers"
	"io"
	"os"
	"sync"
	"time"
)

const version = "1.0.0"
const appName = "goHUserRoleRemover"
const applicationName = "Hornbill Role Removal Utility"

var (
	APIKey        string
	InstanceID    string
	pageSize      int
	mutexTeam     = &sync.Mutex{}
	Users         []map[string]string
	logFile       string
	configCSVFile string
	configDebug   bool
	configHeader  string
	configVersion bool

	configDryRun bool
)

type stateJSONStruct struct {
	Code      string `json:"code"`
	Service   string `json:"service"`
	Operation string `json:"operation"`
	Error     string `json:"error"`
}

type groupListStruct struct {
	GroupName    string
	GroupType    int
	GroupID      string
	GroupMembers map[string]string
}

func NewEspXmlmcSession() *apiLib.XmlmcInstStruct {
	espXmlmcLocal := apiLib.NewXmlmcInstance(InstanceID)
	espXmlmcLocal.SetAPIKey(APIKey)
	espXmlmcLocal.SetTimeout(60)
	espXmlmcLocal.SetJSONResponse(true)
	return espXmlmcLocal
}

func main() {

	flag.StringVar(&APIKey, "api", "", "API Key")
	flag.StringVar(&InstanceID, "instance", "", "Instance ID")
	flag.StringVar(&configCSVFile, "file", "", "CSV File")
	flag.StringVar(&configHeader, "header", "userid", "The header/fieldname in the CSV to use")
	flag.BoolVar(&configDebug, "debug", true, "Debug mode - additional logging")
	flag.BoolVar(&configVersion, "version", false, "Return version and end")
	flag.BoolVar(&configDryRun, "dryrun", false, "Dry Run")
	flag.Parse()

	//-- If configVersion just output version number and die
	if configVersion {
		fmt.Printf("%v \n", version)
		return
	}

	startTime := time.Now()
	logFile = "roleremover_" + time.Now().Format("20060102150405") + ".log"
	hornbillHelpers.Logger(3, "---- "+applicationName+" v"+version+" ----", true, logFile)

	if configCSVFile == "" {
		hornbillHelpers.Logger(4, "No CSV file given", true, logFile)
		flag.PrintDefaults()
		return
	}
	if APIKey == "" {
		hornbillHelpers.Logger(4, "No API set", true, logFile)
		flag.PrintDefaults()
		return
	}
	if InstanceID == "" {
		hornbillHelpers.Logger(4, "No Instance ID set", true, logFile)
		flag.PrintDefaults()
		return
	}

	if _, err := os.Stat(configCSVFile); os.IsNotExist(err) {
		hornbillHelpers.Logger(4, configCSVFile+" does NOT exist", true, logFile)
		flag.PrintDefaults()
		return
	}
	debugLog("Flag - API "+APIKey, false)
	debugLog("Flag - Instance "+InstanceID, false)
	debugLog("Flag - File "+configCSVFile, false)
	debugLog("Flag - PageSize "+fmt.Sprintf("%d", pageSize), false)
	if configDebug {
		debugLog("Flag - Debugging On", false)
	}

	b, Users := getRecordsFromCSV(configCSVFile)
	if !(b) {
		hornbillHelpers.Logger(4, "Issues with loading the CSV file", true, logFile)
		return
	}
	//fmt.Println(Users)

	handleRoles(Users)

	endTime := time.Since(startTime)
	debugLog("Time Taken: "+fmt.Sprintf("%v", endTime), false)
	hornbillHelpers.Logger(3, "---- End of Utility ---- ", true, logFile)

}

func handleRoles(Users []map[string]string) {

	/*
		<methodCall service="admin" method="userGetRoleList">
		<params>
			<userId>XXX</userId>
		</params>
		</methodCall>

		{
			"@status": true,
			"params": {
				"role": [
					"Basic User Role",
					"Coworker Lifecycle",
					"HTLSupport_Import",
					"Knowledge Base Manager"
				]
			}
		}

		<methodCall service="admin" method="userRemoveRole">
		<params>
			<userId>andy.gittos@hornbill.com</userId>
			<role>Basic User Role</role>
			<role>Basic User Role</role>
			<role>Basic User Role</role>
		</params>
		</methodCall>
	*/

	count := len(Users)

	//-- Init Map
	hornbillHelpers.Logger(3, fmt.Sprintf("%d users to process", count), false, logFile)

	//-- Load Results in pages of pageSize
	bar := pb.StartNew(count)

	espXmlmc := NewEspXmlmcSession()

	for _, record := range Users {
		//if current_user_id, ok := record["userid"]; ok {
		current_user_id := record[configHeader]
		espXmlmc.ClearParam()
		espXmlmc.SetParam("userId", current_user_id)

		JSONResp, success := doInvoke(espXmlmc, "admin", "userGetRoleList", true)
		if success && len(JSONResp.Params.Roles) > 0 {
			// adding subscription must have worked - now the old one can be marked for removal
			espXmlmc.ClearParam()
			espXmlmc.SetParam("userId", current_user_id)

			for _, role := range JSONResp.Params.Roles {
				espXmlmc.SetParam("role", role)
			}
			_, success := doInvoke(espXmlmc, "admin", "userRemoveRole", false)
			if !success {
				hornbillHelpers.Logger(4, "Failed Role Removal: "+current_user_id, false, logFile)
			} else {
				hornbillHelpers.Logger(3, "Roles Removed for "+current_user_id, false, logFile)
			}

		} else {
			hornbillHelpers.Logger(3, "Skipped Role Removal: "+current_user_id+fmt.Sprintf(" (%d)", len(JSONResp.Params.Roles)), false, logFile)
		}
		bar.Add(1)
	}

	bar.FinishPrint("Users Processed\n")
	debugLog("Finished Processing Users", false)

}

type jsonMegaStructResponse struct {
	Params struct {
		Group []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"group"`
		MaxPages int    `json:"maxPages"`
		Updated  bool   `json:"recordUpdated"`
		Deleted  bool   `json:"recordDeleted"`
		Added    bool   `json:"recordAdded"`
		ID       string `json:"recordId"`
		Rowdata  struct {
			Row []map[string]string `json:"row"`
		} `json:"rowData"`
		Roles []string `json:"role"`
		Users []struct {
			ID   string `json:"userId"`
			Name string `json:"name"`
		} `json:"userListItem"`
	} `json:"params"`
	State stateJSONStruct `json:"state"`
}

func debugLog(logtext string, cmdOutput bool) {
	if configDebug {
		hornbillHelpers.Logger(1, logtext, cmdOutput, logFile)
	}
}

func doInvoke(espXmlmcLocal *apiLib.XmlmcInstStruct, service string, method string, leavesUnmodified bool) (jsonMegaStructResponse, bool) {
	debugLog(espXmlmcLocal.GetParam(), false)
	var JSONResp jsonMegaStructResponse

	if configDryRun && !leavesUnmodified {
		debugLog("Dry Run => Skipping "+service+"::"+method, false)
		return JSONResp, false
	}

	RespBody, xmlmcErr := espXmlmcLocal.Invoke(service, method)
	debugLog(RespBody, false)

	if xmlmcErr != nil {
		hornbillHelpers.Logger(4, "Unable to get "+service+"::"+method+" :- "+xmlmcErr.Error(), false, logFile)
		return JSONResp, false
	}
	err := json.Unmarshal([]byte(RespBody), &JSONResp)
	if err != nil {
		hornbillHelpers.Logger(4, "Unable to read "+service+"::"+method+" :- "+err.Error(), false, logFile)
		return JSONResp, false
	}
	if JSONResp.State.Error != "" {
		hornbillHelpers.Logger(4, "Unable to get "+service+"::"+method+" :- "+JSONResp.State.Error, false, logFile)
		return JSONResp, false
	}
	return JSONResp, true
}

func getRecordsFromCSV(csvFile string) (bool, []map[string]string) {
	rows := []map[string]string{}
	file, err := os.Open(csvFile)
	if err != nil {
		hornbillHelpers.Logger(4, "Error opening CSV file: "+err.Error(), true, logFile)
		return false, rows
	}
	defer file.Close()

	bom := make([]byte, 3)
	file.Read(bom)
	if bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		// BOM Detected, continue with feeding the file
	} else {
		// No BOM Detected, reset the file feed
		file.Seek(0, 0)
	}

	r := csv.NewReader(file)
	r.LazyQuotes = true

	var header []string

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			hornbillHelpers.Logger(4, "Error reading CSV data: "+err.Error(), true, logFile)
			return false, rows
		}
		if header == nil {
			header = record
		} else {
			dict := map[string]string{}
			for i := range header {
				dict[header[i]] = record[i]
			}
			rows = append(rows, dict)
		}
	}
	return true, rows

}
