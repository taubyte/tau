package main

// TODO: Revisit

// import (
// 	"fmt"
// 	"io"
// 	"os"

// 	"context"

// 	"bitbucket.org/taubyte/spore-drive/ifaces"
// 	"bitbucket.org/taubyte/spore-drive/pilots/booker"
// )

// var PackageName = "billing"

// func main() {
// 	ctx, ctxC := context.WithCancel(context.Background())
// 	defer ctxC()

// 	pilot, err := booker.New(ctx, &booker.Config{
// 		Package: booker.PackageConfig{
// 			Name: PackageName,
// 		},
// 		SystemD: booker.SystemDConfig{
// 			Name: "tb" + PackageName,
// 		},
// 		Build: booker.BuildConfig{
// 			Path: "../../cmd",
// 		},
// 	})

// 	if err != nil {
// 		panic(err)
// 	}

// 	course, err := pilot.Plot("unas", ifaces.Service(PackageName))
// 	if err != nil {
// 		panic(err)
// 	}

// 	out, errs := pilot.Displace(course)
// 	errStr := ""
// 	for e := range errs {
// 		errStr += e.Error()
// 	}
// 	if len(errs) > 0 {
// 		panic(errStr)
// 	}

// 	io.Copy(os.Stdout, out)
// 	fmt.Println("---------------")
// }
