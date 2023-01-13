package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	"github.com/wangning057/remote_executor/cmd_util"
	pb "github.com/wangning057/remote_executor/ninja_grpc"
	"google.golang.org/grpc"
)

var (
	port = flag.Int("port", 50051, "The server port")
)

var count int

// server is used to implement ninja_grpc.ExecutorServer.
type server struct {
	pb.UnimplementedExecutorServer
}

// Execute implements ninja_grpc.ExecutorServer
func (s *server) Execute(ctx context.Context, in *pb.Command) (*pb.ExecuteResult, error) {

	cmd := &cmd_util.Command{
		Content:     in.GetStrCommand(),
		Env:         make([]string, 0),
		Use_console: false,
	}

	workDir := "/home/ubuntu/LLVM/build"

	var stdout, stderr bytes.Buffer
	stdio := &cmd_util.Stdio{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	}
	res := &pb.ExecuteResult{}

	// 好像不用开协程啊，这里是服务端，来一个服务一个，本来就是多进程的？？？？？？
	// go func() {
	// 	cmd_util.Run(ctx, cmd, workDir, stdio)
	// 	res.HasFinished = "ok"
	// 	ctx.Done();
	// 	count += 1
	// 	fmt.Println("已完成任务数： ", count)
	// }()

	cmd_util.Run(ctx, cmd, workDir, stdio)
	res.HasFinished = "ok"
	ctx.Done()
	count += 1
	fmt.Println("已完成任务数： ", count)

	//下面是测试
	// cmdStr := in.GetStrCommand()
	// log.Println(cmdStr)
	// cmd := exec.Command("/bin/bash", "-c", cmdStr)
	// // cmd := exec.Command(cmdStr)
	// cmd.Dir = "/home/ning/dev/ninja_original/"
	// var stderr, stdout bytes.Buffer
	// cmd.Stderr = &stderr
	// cmd.Stdout = &stdout
	// err := cmd.Start()
	// if err != nil {
	// 	fmt.Println("cmd.Start();出错：", err)
	// }
	// cmd.Wait()
	// fmt.Println("执行完成!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	// fmt.Println("stderr =============== ", stderr)
	// fmt.Println("stdout =============== ", stdout)
	// res := &pb.ExecuteResult{}
	// res.HasFinished = "ok"

	// ctx.Done()

	return res, nil
}

func Execute_Test() {
	ctx := context.Background()

	cmd := &cmd_util.Command{
		Content:     "ls -a",
		Env:         make([]string, 0),
		Use_console: false,
	}

	workDir := "/home/ubuntu/LLVM"

	var stdout, stderr bytes.Buffer
	stdio := &cmd_util.Stdio{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	cmd_util.Run(ctx, cmd, workDir, stdio)

	fmt.Println(stdio.Stdout)

}

func main() {

	Execute_Test()

	count = 0

	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterExecutorServer(s, &server{})
	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
