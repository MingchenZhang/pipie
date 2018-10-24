package internal

import (
	"bytes"
	"context"
	"errors"
	"gopkg.in/tomb.v2"
	"os"
	"syscall"
)

func PipeInlet(ctx context.Context, commonSession muxSession) (error) {
	var err error
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	ignoreSignalDefault(syscall.SIGPIPE)
	defer unIgnoreSignalDefault(syscall.SIGPIPE)
	ignoreSignalDefault(syscall.SIGINT)
	defer unIgnoreSignalDefault(syscall.SIGINT)
	defer func() {
		if err := recover(); err != nil {
			panic(err)
		}
		t.Kill(nil)
		t.Wait()
	}()
	go func() {
		// handle signal goroutine
		select {
		case <- signalSIGINT:
			t.Kill(nil)
		case <- t.Dead():
		}
	}()
	theStream, err := commonSession.OpenStream()
	if err != nil {
		log.Errorf("cannot open stream on common session")
		return err
	}
	defer func() {
		log.Debugf("to close")
		err = theStream.Close()
		log.Debugf("to close return")
		if err != nil{
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
	stdin := os.NewFile(0, "stdin")
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

	<- t.Dying()
	log.Debug("pipe closing")

	return nil
}
