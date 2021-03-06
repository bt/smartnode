package rocketpool

import (
    "bufio"
    "errors"
    "fmt"
    "io"
    "io/ioutil"
    "os"
    "strings"

    "github.com/fatih/color"
    "github.com/urfave/cli"
    "golang.org/x/crypto/ssh"

    "github.com/rocket-pool/smartnode/shared/services/config"
    "github.com/rocket-pool/smartnode/shared/utils/net"
)


// Config
const (
    InstallerURL = "https://github.com/rocket-pool/smartnode-install/releases/latest/download/install.sh"

    RocketPoolPath = "~/.rocketpool"
    GlobalConfigFile = "config.yml"
    UserConfigFile = "settings.yml"
    ComposeFile = "docker-compose.yml"

    APIContainerName = "rocketpool_api"
    APIBinPath = "/go/bin/rocketpool"

    DebugColor = color.FgYellow
)


// Rocket Pool client
type Client struct {
    client *ssh.Client
}


// Create new Rocket Pool client from CLI context
func NewClientFromCtx(c *cli.Context) (*Client, error) {
    return NewClient(c.GlobalString("host"), c.GlobalString("user"), c.GlobalString("key"))
}


// Create new Rocket Pool client
func NewClient(hostAddress, user, keyPath string) (*Client, error) {

    // Initialize SSH client if configured for SSH
    var sshClient *ssh.Client
    if (hostAddress != "") {

        // Check parameters
        if user == "" {
            return nil, errors.New("The SSH user (--user) must be specified.")
        }
        if keyPath == "" {
            return nil, errors.New("The SSH private key path (--key) must be specified.")
        }

        // Read private key
        keyBytes, err := ioutil.ReadFile(keyPath)
        if err != nil {
            return nil, fmt.Errorf("Could not read SSH private key at %s: %w", keyPath, err)
        }

        // Parse private key
        key, err := ssh.ParsePrivateKey(keyBytes)
        if err != nil {
            return nil, fmt.Errorf("Could not parse SSH private key at %s: %w", keyPath, err)
        }

        // Initialise client
        sshClient, err = ssh.Dial("tcp", net.DefaultPort(hostAddress, "22"), &ssh.ClientConfig{
            User: user,
            Auth: []ssh.AuthMethod{ssh.PublicKeys(key)},
            HostKeyCallback: ssh.InsecureIgnoreHostKey(),
        })
        if err != nil {
            return nil, fmt.Errorf("Could not connect to %s as %s: %w", hostAddress, user, err)
        }

    }

    // Return client
    return &Client{
        client: sshClient,
    }, nil

}


// Close client remote connection
func (c *Client) Close() {
    if c.client != nil {
        c.client.Close()
    }
}


// Load the global config
func (c *Client) LoadGlobalConfig() (config.RocketPoolConfig, error) {
    return c.loadConfig(fmt.Sprintf("%s/%s", RocketPoolPath, GlobalConfigFile))
}


// Save the user config
func (c *Client) SaveUserConfig(cfg config.RocketPoolConfig) error {
    return c.saveConfig(cfg, fmt.Sprintf("%s/%s", RocketPoolPath, UserConfigFile))
}


// Install the Rocket Pool service
func (c *Client) InstallService(verbose, noDeps bool, network, version string) error {

    // Get installation script downloader type
    downloader, err := c.getDownloader()
    if err != nil { return err }

    // Get installation script flags
    flags := []string{
        "-n", network,
        "-v", version,
    }
    if noDeps {
        flags = append(flags, "-d")
    }

    // Initialize installation command
    cmd, err := c.newCommand(fmt.Sprintf("%s %s | sh -s -- %s", downloader, InstallerURL, strings.Join(flags, " ")))
    if err != nil { return err }
    defer cmd.Close()

    // Get command output pipes
    cmdOut, err := cmd.StdoutPipe()
    if err != nil { return err }
    cmdErr, err := cmd.StderrPipe()
    if err != nil { return err }

    // Print progress from stdout
    go (func() {
        scanner := bufio.NewScanner(cmdOut)
        for scanner.Scan() {
            fmt.Println(scanner.Text())
        }
    })()

    // Read command & error output from stderr; render in verbose mode
    var errMessage string
    go (func() {
        c := color.New(DebugColor)
        scanner := bufio.NewScanner(cmdErr)
        for scanner.Scan() {
            errMessage = scanner.Text()
            if verbose {
                c.Println(scanner.Text())
            }
        }
    })()

    // Run command and return error output
    err = cmd.Run()
    if err != nil {
        return fmt.Errorf("Could not install Rocket Pool service: %s", errMessage)
    }
    return nil

}


// Start the Rocket Pool service
func (c *Client) StartService() error {
    cmd, err := c.compose("up -d")
    if err != nil { return err }
    return c.printOutput(cmd)
}


// Pause the Rocket Pool service
func (c *Client) PauseService() error {
    cmd, err := c.compose("stop")
    if err != nil { return err }
    return c.printOutput(cmd)
}


// Stop the Rocket Pool service
func (c *Client) StopService() error {
    cmd, err := c.compose("down -v")
    if err != nil { return err }
    return c.printOutput(cmd)
}


// Print the Rocket Pool service status
func (c *Client) PrintServiceStatus() error {
    cmd, err := c.compose("ps")
    if err != nil { return err }
    return c.printOutput(cmd)
}


// Print the Rocket Pool service logs
func (c *Client) PrintServiceLogs(tail string, serviceNames ...string) error {
    cmd, err := c.compose(fmt.Sprintf("logs -f --tail %s %s", tail, strings.Join(serviceNames, " ")))
    if err != nil { return err }
    return c.printOutput(cmd)
}


// Print the Rocket Pool service stats
func (c *Client) PrintServiceStats() error {

    // Get service container IDs
    cmd, err := c.compose("ps -q")
    if err != nil { return err }
    containers, err := c.readOutput(cmd)
    if err != nil { return err }
    containerIds := strings.Split(strings.TrimSpace(string(containers)), "\n")

    // Print stats
    return c.printOutput(fmt.Sprintf("docker stats %s", strings.Join(containerIds, " ")))

}


// Load a config file
func (c *Client) loadConfig(path string) (config.RocketPoolConfig, error) {
    configBytes, err := c.readOutput(fmt.Sprintf("cat %s", path))
    if err != nil {
        return config.RocketPoolConfig{}, fmt.Errorf("Could not read Rocket Pool config at %s: %w", path, err)
    }
    return config.Parse(configBytes)
}


// Save a config file
func (c *Client) saveConfig(cfg config.RocketPoolConfig, path string) error {
    configBytes, err := cfg.Serialize()
    if err != nil {
        return err
    }
    if _, err := c.readOutput(fmt.Sprintf("cat > %s <<EOF\n%sEOF", path, string(configBytes))); err != nil {
        return fmt.Errorf("Could not write Rocket Pool config to %s: %w", path, err)
    }
    return nil
}


// Build a docker-compose command
func (c *Client) compose(args string) (string, error) {

    // Load config
    globalConfig, err := c.loadConfig(fmt.Sprintf("%s/%s", RocketPoolPath, GlobalConfigFile))
    if err != nil {
        return "", err
    }
    userConfig, err := c.loadConfig(fmt.Sprintf("%s/%s", RocketPoolPath, UserConfigFile))
    if err != nil {
        return "", err
    }
    rpConfig := config.Merge(&globalConfig, &userConfig)

    // Check config
    if rpConfig.GetSelectedEth1Client() == nil {
        return "", errors.New("No Eth 1.0 client selected. Please run 'rocketpool service config' and try again.")
    }
    if rpConfig.GetSelectedEth2Client() == nil {
        return "", errors.New("No Eth 2.0 client selected. Please run 'rocketpool service config' and try again.")
    }

    // Set environment variables from config
    env := []string{
        "COMPOSE_PROJECT_NAME=rocketpool",
        fmt.Sprintf("ETH1_CLIENT=%s",      rpConfig.GetSelectedEth1Client().ID),
        fmt.Sprintf("ETH1_IMAGE=%s",       rpConfig.GetSelectedEth1Client().Image),
        fmt.Sprintf("ETH2_CLIENT=%s",      rpConfig.GetSelectedEth2Client().ID),
        fmt.Sprintf("ETH2_IMAGE=%s",       rpConfig.GetSelectedEth2Client().GetBeaconImage()),
        fmt.Sprintf("VALIDATOR_CLIENT=%s", rpConfig.GetSelectedEth2Client().ID),
        fmt.Sprintf("VALIDATOR_IMAGE=%s",  rpConfig.GetSelectedEth2Client().GetValidatorImage()),
        fmt.Sprintf("ETH1_PROVIDER=%s",    rpConfig.Chains.Eth1.Provider),
        fmt.Sprintf("ETH2_PROVIDER=%s",    rpConfig.Chains.Eth2.Provider),
    }
    for _, param := range rpConfig.Chains.Eth1.Client.Params {
        env = append(env, fmt.Sprintf("%s=%s", param.Env, param.Value))
    }
    for _, param := range rpConfig.Chains.Eth2.Client.Params {
        env = append(env, fmt.Sprintf("%s=%s", param.Env, param.Value))
    }

    // Return command
    return fmt.Sprintf("%s docker-compose --project-directory %s -f %s %s", strings.Join(env, " "), RocketPoolPath, fmt.Sprintf("%s/%s", RocketPoolPath, ComposeFile), args), nil

}


// Call the Rocket Pool API
func (c *Client) callAPI(args string) ([]byte, error) {
    return c.readOutput(fmt.Sprintf("docker exec %s %s api %s", APIContainerName, APIBinPath, args))
}


// Get the first downloader available to the system
func (c *Client) getDownloader() (string, error) {

    // Check for cURL
    hasCurl, err := c.readOutput("command -v curl")
    if err == nil && len(hasCurl) > 0 {
        return "curl -sL", nil
    }

    // Check for wget
    hasWget, err := c.readOutput("command -v wget")
    if err == nil && len(hasWget) > 0 {
        return "wget -qO-", nil
    }

    // Return error
    return "", errors.New("Either cURL or wget is required to begin installation.")

}


// Run a command and print its output
func (c *Client) printOutput(cmdText string) error {

    // Initialize command
    cmd, err := c.newCommand(cmdText)
    if err != nil { return err }
    defer cmd.Close()

    // Copy command output to stdout & stderr
    cmdOut, err := cmd.StdoutPipe()
    if err != nil { return err }
    cmdErr, err := cmd.StderrPipe()
    if err != nil { return err }
    go io.Copy(os.Stdout, cmdOut)
    go io.Copy(os.Stderr, cmdErr)

    // Run command
    return cmd.Run()

}


// Run a command and return its output
func (c *Client) readOutput(cmdText string) ([]byte, error) {

    // Initialize command
    cmd, err := c.newCommand(cmdText)
    if err != nil {
        return []byte{}, err
    }
    defer cmd.Close()

    // Run command and return output
    return cmd.Output()

}

