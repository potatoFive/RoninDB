@echo off

echo --- Step 0: Cleaning Old Files ---
:: Remove the database and csv files from the current directory and the files folder
if exist "..\RoninWldToDB\ronin.db" del /q "..\RoninWldToDB\ronin.db"
if exist "..\RoninWldToDB\*.csv" del /q "..\RoninWldToDB\*.csv"

:: Optional: Clean the source folder where the Go script generates them
if exist "..\RoninWldToDB\ronin.db" del /q "..\RoninWldToDB\ronin.db"

echo --- Step 1: Generating Database ---
pushd "..\RoninWldToDB"
go run RoninWldToDB.go
popd

echo --- Step 2: Building Web Server ---
pushd "..\RoninDB"
go build -o eqdb.exe RoninDB.go
popd

echo --- Step 3: Copying Files ---
:: Copy DB to current directory
xcopy "..\RoninWldToDB\ronin.db" "." /y /i

:: Copy DB and CSVs to the local files folder
xcopy "..\ronin\lib\tinyworld.idx" "\" /y /i
xcopy "..\RoninWldToDB\ronin.db" "files\" /y /i
xcopy "..\RoninWldToDB\obj.csv" "files\" /y /i
xcopy "..\RoninWldToDB\mob.csv" "files\" /y /i
xcopy "..\RoninWldToDB\zon.csv" "files\" /y /i

echo --- Done! ---
pause