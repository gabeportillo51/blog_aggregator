package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/gabeportillo51/blog_aggregator/internal/config"
	"github.com/gabeportillo51/blog_aggregator/internal/database"
	_ "github.com/lib/pq"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Error: no command argument provided.")
		os.Exit(1)
	}
	user_cmd := config.Command{
		Name: os.Args[1],
		Args: os.Args[2:],
	}
	config_struct := config.Read()
	db, err := sql.Open("postgres", config_struct.DBUrl)
	if err != nil {
		fmt.Printf("Error occured: %s\n", err)
		os.Exit(1)
	}
	var main_state config.State
	main_state.Cfg = &config_struct
	dbQueries := database.New(db)
	main_state.Db = dbQueries
	command_registry := config.Commands{
		Registry: make(map[string]func(*config.State, config.Command) error),
	}
	command_registry.Register("login", config.HandlerLogin)
	command_registry.Register("register", config.HandlerRegister)
	command_registry.Register("reset", config.HandlerReset)
	command_registry.Register("users", config.HandlerListUsers)
	command_registry.Register("agg", config.HandlerAgg)
	command_registry.Register("addfeed", config.HandlerAddFeed)
	err1 := command_registry.Run(&main_state, user_cmd)
	if err1 != nil {
		fmt.Println(err1)
		os.Exit(1)
	}
}
