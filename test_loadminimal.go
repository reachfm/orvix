package main

import (
	"fmt"
	"os"
	"path/filepath"
	"github.com/spf13/viper"
)

func main() {
	v := viper.New()
	v.SetConfigName("orvix")
	v.SetConfigType("yaml")

	configPaths := []string{
		".",
		"./configs",
		"/etc/orvix",
		filepath.Join(os.Getenv("HOME"), ".orvix"),
		os.Getenv("ORVIX_CONFIG_DIR"),
	}
	
	fmt.Println("Searching config paths:")
	for _, p := range configPaths {
		fmt.Printf("  %q (empty=%v)\n", p, p == "")
		if p != "" {
			v.AddConfigPath(p)
		}
	}
	
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			fmt.Println("ConfigFileNotFoundError (expected)")
		} else {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Config found: " + v.ConfigFileUsed())
	}
}
