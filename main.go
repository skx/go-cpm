// cpmulator entry-point / driver

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/skx/cpmulator/cpm"
)

func main() {

	//
	// Parse the command-line flags for this driver-application
	//
	cd := flag.String("cd", "", "Change to this directory before launching")
	useDirectories := flag.Bool("directories", false, "Use subdirectories on the host computer for CP/M drives.")
	createDirectories := flag.Bool("create", false, "Create subdirectories on the host computer for each CP/M drive.")
	flag.Parse()

	// Default program to execute, and arguments to pass to program
	program := ""
	args := []string{}

	// If we have a program
	if len(flag.Args()) > 0 {
		program = flag.Args()[0]
		if len(flag.Args()) > 1 {
			args = flag.Args()[1:]
		}
	}

	// Setup our logging level - default to warnings or higher.
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelWarn)

	// But show everything if $DEBUG is non-empty.
	if os.Getenv("DEBUG") != "" {
		lvl.Set(slog.LevelDebug)
	}

	// Create our logging handler, using the level we've just setup
	log := slog.New(
		slog.NewJSONHandler(
			os.Stderr,
			&slog.HandlerOptions{
				Level: lvl,
			}))

	// Create a new emulator.
	obj := cpm.New(log)

	// change directory?
	if *cd != "" {
		err := os.Chdir(*cd)
		if err != nil {
			fmt.Printf("failed to change to %s:%s\n", *cd, err)
			return
		}
	}

	// Should we create child-directories?  If so, do so.
	if *createDirectories {
		for _, d := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P"} {
			if _, err := os.Stat(d); os.IsNotExist(err) {
				_ = os.Mkdir(d, 0755)
			}
		}
	}

	// Are we using drives?
	if *useDirectories {

		// Enable drives
		obj.SetDrives(true)

		// Count how many drives exist - if zero show a warning
		found := 0
		for _, d := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P"} {
			if _, err := os.Stat(d); err == nil {
				found++
			}
		}
		if found == 0 {
			fmt.Printf("WARNING: You've chosen to  directories as drives.\n")
			fmt.Printf("         i.e. A/ would be used for the contents of A:\n")
			fmt.Printf("         i.e. B/ would be used for the contents of B:\n")
			fmt.Printf("\n")
			fmt.Printf("No drive-directories are present, you could fix this:\n")
			fmt.Printf("         mkdir A\n")
			fmt.Printf("         mkdir B\n")
			fmt.Printf("         mkdir C\n")
			fmt.Printf("         etc\n")
			fmt.Printf("\n")
			fmt.Printf("Run this program with '-create' to automatically create these directories.\n")
		}
	}

	// Load the binary, if we were given one.
	if program != "" {

		err := obj.LoadBinary(program)
		if err != nil {
			fmt.Printf("Error loading program %s:%s\n", program, err)
			return
		}

		err = obj.Execute(args)
		if err != nil {

			// Deliberate stop of execution
			if err == cpm.ErrHalt {
				return
			}
			// Deliberate stop of execution.
			if err == cpm.ErrExit {
				return
			}

			fmt.Printf("Error running %s [%s]: %s\n",
				program, strings.Join(args, ","), err)
			return
		}
	} else {
		// The drive will default to A:, or 0.
		var drive uint8

		// We load and re-run eternally - because many binaries the CCP
		// would launch would end with "exit" which would otherwise cause
		// our emulation to terminate
		//
		//
		for {
			// Load the CCP binary - reseting RAM
			obj.LoadCCP()

			// Set the current drive.
			obj.SetCurrentDrive(drive)

			// Run the CCP, which will often load a child-binary.
			// The child-binary will call "P_TERMCPM" which will cause
			// the CCP to terminate.
			err := obj.Execute(args)
			if err != nil {

				// Deliberate stop of execution.
				if err == cpm.ErrHalt {
					return
				}

				fmt.Printf("\nError running CCP: %s\n", err)
				return
			}

			// Get the drive, so that if the user changed it and we
			// secretly restart the execution afresh after the child has
			// terminated their drive persists.
			//
			// NOTE: UserNumber will reset to zero, but we don't use that..
			drive = obj.GetCurrentDrive()

		}
	}

}
