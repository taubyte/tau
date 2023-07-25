CREATE TABLE `Services` ( `Id` varchar(255), `Timestamp` int, `Type` int);

CREATE TABLE `Usage` ( `Id` varchar(255) UNIQUE, `Timestamp` int, `UsedMemory` int, `TotalMemory` int, `TotalCpu` int, `CpuCount` int);

CREATE TABLE `Meta` ( `Id` varchar(255), `Type` int, `Key` varchar(255), `Value` varchar(255));