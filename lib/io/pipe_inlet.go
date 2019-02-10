package io

import (
	"bytes"
	"context"
	"errors"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
	"gopkg.in/tomb.v2"
	"os"
)

func PipeInlet(ctx context.Context, commonSession pairconnect.MuxSession) error {
	var err error
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	//signal.IgnoreSignalDefault(syscall.SIGPIPE)
	//defer signal.UnIgnoreSignalDefault(syscall.SIGPIPE)
	//signal.IgnoreSignalDefault(syscall.SIGINT)
	//defer signal.UnIgnoreSignalDefault(syscall.SIGINT)
	defer func() {
		t.Kill(nil)
		t.Wait()
	}()
	//go func() {
	//	// handle signal goroutine
	//	select {
	//	case <- signal.SignalSIGINT:
	//		t.Kill(nil)
	//	case <- t.Dead():
	//	}
	//}()
	theStream, err := commonSession.Open()
	if err != nil {
		log.Errorf("cannot open stream on common session")
		return err
	}
	defer func() {
		log.Debugf("closing stream")
		err = theStream.Close()
		log.Debugf("stream closed")
		if err != nil {
			log.Warning(err)
		}
	}()
	// confirm each side identity
	{
		n, err := theStream.Write([]byte(ModePipeInlet))
		if err != nil || n != ModeStringLen {
			log.Error("unable to send identity")
			return errors.New("unable to send identity")
		}
		var buf = make([]byte, ModeStringLen)
		_, err = theStream.Read(buf)
		if err != nil || !bytes.Equal(buf, []byte(ModePipeOutlet)) {
			log.Error("unable to confirm peer identity")
			return errors.New("unable to confirm peer identity")
		}
		log.Debug("identity confirmed")
	}

	// prepare stdin
	//if err := syscall.SetNonblock(syscall.Stdin, true); err != nil {
	//	log.Warning("syscall.Stdin cannot be set to unblocking")
	//}
	//stdin := os.NewFile(0, "stdin")
	stdin := os.Stdin
	defer func() {
		// actually no point in closing stdin. stdin is blocking , so it does not support setDeadline. closing it would not unblock read
		//stdin.Close()
		//if err := syscall.SetNonblock(syscall.Stdin, false); err != nil {
		//	log.Warning("syscall.Stdin cannot be set back to blocking")
		//}
	}()

	t.Go(func() error {
		defer t.Kill(nil)
		ReaderToConn(stdin, theStream, true)
		return nil
	})

	<-t.Dying()
	log.Debug("pipe closing")

	return nil
}
