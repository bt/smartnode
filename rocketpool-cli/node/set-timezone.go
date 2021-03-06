package node

import (
    "fmt"

    "github.com/urfave/cli"

    "github.com/rocket-pool/smartnode/shared/services/rocketpool"
)


func setTimezoneLocation(c *cli.Context) error {

    // Get RP client
    rp, err := rocketpool.NewClientFromCtx(c)
    if err != nil { return err }
    defer rp.Close()

    // Prompt for timezone location
    timezoneLocation := promptTimezone()

    // Set node's timezone location
    if _, err := rp.SetNodeTimezone(timezoneLocation); err != nil {
        return err
    }

    // Log & return
    fmt.Printf("The node's timezone location was successfully updated to '%s'.\n", timezoneLocation)
    return nil

}

